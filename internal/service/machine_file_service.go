package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/filestore"
	"github.com/FredrickUnderwood/agenda-v2/internal/git"
	"github.com/FredrickUnderwood/agenda-v2/internal/logger"
	"github.com/FredrickUnderwood/agenda-v2/internal/repository"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// ErrNoMachinesForEnv is returned when an environment has no instance to
// deliver a file to. It is a distinct error because the fix is a different
// action entirely — add an instance — rather than a correction to the upload.
var ErrNoMachinesForEnv = errors.New("this environment has no enabled instances, so there is no machine to upload to")

// ErrMachineFileNotFound covers "no such upload record".
var ErrMachineFileNotFound = errors.New("file record not found")

// ErrNoWorkspaceRoot is returned when a machine has no workspace root, its own
// or inherited. Without one there is no directory agenda is entitled to write
// to on that machine, so an upload has nowhere legitimate to go.
var ErrNoWorkspaceRoot = errors.New("this machine has no workspace_root configured, so there is no directory agenda may upload into")

// fileNamePattern constrains the name of an app-environment file. The name
// becomes a path component on every machine in the environment and a filename
// inside every container, so it is restricted to what is unambiguous in both:
// no separators, no shell metacharacters, no leading dash.
var fileNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,254}$`)

// FileOpener yields a fresh reader over the uploaded content each time it is
// called. Uploads fan out to every machine in an environment, and a single
// io.Reader can only be consumed once — so the source is expressed as something
// re-openable rather than streamed into memory and replayed.
type FileOpener func() (io.ReadCloser, error)

// MachineFileService uploads files to machines through the same runner
// abstraction deploys use, and records what it wrote so the console can later
// ask whether the file is still there and still unchanged.
type MachineFileService struct {
	files    *repository.MachineFileRepository
	apps     *repository.ApplicationRepository
	targets  *repository.ApplicationTargetRepository
	machines *MachineService
}

func NewMachineFileService(
	files *repository.MachineFileRepository,
	apps *repository.ApplicationRepository,
	targets *repository.ApplicationTargetRepository,
	machines *MachineService,
) *MachineFileService {
	return &MachineFileService{files: files, apps: apps, targets: targets, machines: machines}
}

// UploadResult is the per-machine outcome of one upload. An environment upload
// touches several machines and any of them can fail on its own, so the API
// reports each separately instead of collapsing to one status.
type UploadResult struct {
	MachineID   int64               `json:"machine_id"`
	MachineName string              `json:"machine_name"`
	Path        string              `json:"path"`
	Success     bool                `json:"success"`
	Error       string              `json:"error,omitempty"`
	File        *domain.MachineFile `json:"file,omitempty"`
}

// UploadToAppEnv writes one file to every machine hosting an enabled instance
// of (app, env), under that environment's managed file directory, and records
// one row per machine that accepted it.
//
// A machine that fails gets no row. A row asserts "this file is on that machine
// with this checksum", and writing one for a failed transfer would both lie and
// break verification: the failed row is the newest for its path, so it would
// become the "current" record and report the real file as changed.
func (s *MachineFileService) UploadToAppEnv(
	ctx context.Context,
	p Principal,
	appID int64,
	env domain.Environment,
	fileName, mode string,
	overwrite bool,
	open FileOpener,
) ([]UploadResult, error) {
	if !fileNamePattern.MatchString(fileName) {
		return nil, errors.New("file name must start with a letter, digit or underscore and contain only letters, digits, '.', '_' and '-'")
	}
	if !env.Valid() {
		return nil, fmt.Errorf("invalid env %q", env)
	}
	if _, err := filestore.ParseMode(mode); err != nil {
		return nil, err
	}
	app, err := s.apps.GetByID(ctx, appID)
	if err != nil {
		return nil, err
	}
	machines, err := s.envMachines(ctx, appID, env)
	if err != nil {
		return nil, err
	}
	if len(machines) == 0 {
		return nil, ErrNoMachinesForEnv
	}

	results := make([]UploadResult, 0, len(machines))
	for _, m := range machines {
		dir, err := s.envFilesDir(m, app.Name, env)
		if err != nil {
			results = append(results, UploadResult{MachineID: m.ID, MachineName: m.Name, Error: err.Error()})
			continue
		}
		target := path.Join(dir, fileName)
		rec, err := s.upload(ctx, p, m, target, fileName, mode, overwrite, open, func(f *domain.MachineFile) {
			f.Scope = domain.FileScopeAppEnv
			f.ApplicationID = app.ID
			f.AppName = app.Name
			f.Env = env
		})
		res := UploadResult{MachineID: m.ID, MachineName: m.Name, Path: target, Success: err == nil, File: rec}
		if err != nil {
			res.Error = err.Error()
		}
		results = append(results, res)
	}
	return results, nil
}

// UploadToMachine writes one file to an operator-chosen path on one machine.
// Nothing mounts the result anywhere — see domain.FileScopeMachine.
//
// The path must be inside the machine's workspace root. That is not tidiness:
// agenda-node commonly runs in a container with only the workspace root
// bind-mounted from the host, so a write anywhere else lands inside the node's
// own container, reports success, verifies as OK, and vanishes on the next node
// restart — a file that appears to exist and does not. Refusing the path
// outright is the only outcome that is not a lie.
func (s *MachineFileService) UploadToMachine(
	ctx context.Context,
	p Principal,
	machineID int64,
	targetPath, mode string,
	overwrite bool,
	open FileOpener,
) (*domain.MachineFile, error) {
	clean, err := filestore.ValidatePath(targetPath, nil)
	if err != nil {
		return nil, err
	}
	if _, err := filestore.ParseMode(mode); err != nil {
		return nil, err
	}
	m, err := s.machines.Get(ctx, machineID)
	if err != nil {
		return nil, err
	}
	root := s.machines.EffectiveWorkspaceRoot(m)
	if root == "" {
		return nil, ErrNoWorkspaceRoot
	}
	if !filestore.WithinRoot(clean, root) {
		return nil, fmt.Errorf("%w: %s is outside %s", filestore.ErrOutsideRoots, clean, root)
	}
	return s.upload(ctx, p, m, clean, path.Base(clean), mode, overwrite, open, func(f *domain.MachineFile) {
		f.Scope = domain.FileScopeMachine
	})
}

// upload performs one machine's transfer and records it.
func (s *MachineFileService) upload(
	ctx context.Context,
	p Principal,
	m *domain.Machine,
	targetPath, fileName, mode string,
	overwrite bool,
	open FileOpener,
	decorate func(*domain.MachineFile),
) (*domain.MachineFile, error) {
	src, err := open()
	if err != nil {
		return nil, err
	}
	defer src.Close()

	r := runner.New(s.machines.ToMachineConfig(m))
	stat, err := r.PutFile(ctx, targetPath, src, mode, overwrite)
	if err != nil {
		logger.L().Warn("file upload failed",
			zap.String("machine", m.Name), zap.String("path", targetPath), zap.Error(err))
		return nil, err
	}

	now := time.Now()
	rec := &domain.MachineFile{
		MachineID:   m.ID,
		MachineName: m.Name,
		Path:        targetPath,
		FileName:    fileName,
		Size:        stat.Size,
		SHA256:      stat.SHA256,
		Mode:        stat.Mode,
		UserID:      p.UserID,
		Username:    p.Username,
		// The upload itself is the first verification: the checksum came back
		// from the machine that now holds the file.
		LastVerifiedAt:   &now,
		LastVerifyStatus: domain.FileVerifyOK,
		LastVerifySHA256: stat.SHA256,
	}
	decorate(rec)
	if err := s.files.Create(ctx, rec); err != nil {
		return nil, err
	}
	rec.Current = true
	return rec, nil
}

// envMachines returns the distinct machines hosting enabled instances of
// (app, env). A decommissioned instance still counts: its machine holds the
// environment's file directory and will need the file when it is redeployed.
func (s *MachineFileService) envMachines(ctx context.Context, appID int64, env domain.Environment) ([]*domain.Machine, error) {
	targets, err := s.targets.ListByApplication(ctx, appID)
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]bool)
	var out []*domain.Machine
	for _, t := range targets {
		if t.Env != env || !t.Enabled || t.MachineID <= 0 || seen[t.MachineID] {
			continue
		}
		seen[t.MachineID] = true
		m, err := s.machines.Get(ctx, t.MachineID)
		if err != nil {
			logger.L().Warn("skipping machine that could not be loaded",
				zap.Int64("machine_id", t.MachineID), zap.Error(err))
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// envFilesDir resolves the managed file directory for (app, env) on a machine.
// The root comes from MachineService so it matches what the console shows and
// what UploadToMachine enforces; the deploy pipeline applies the same
// precedence, so the upload and the bind mount always agree.
func (s *MachineFileService) envFilesDir(m *domain.Machine, appName string, env domain.Environment) (string, error) {
	root := s.machines.EffectiveWorkspaceRoot(m)
	if root == "" {
		return "", ErrNoWorkspaceRoot
	}
	return git.EnvFilesDir(root, appName, string(env), ToMachineConfig(m).IsLocal())
}

// ContainerPath is where a file uploaded under FileScopeAppEnv appears inside
// the application's containers — the value an app's own config should point at.
func ContainerPath(fileName string) string {
	return path.Join(contract.AgendaContainerFilesDir, fileName)
}

// ─── Reading ─────────────────────────────────────────────────────────────

func (s *MachineFileService) ListForAppEnv(ctx context.Context, appID int64, env domain.Environment) ([]*domain.MachineFile, error) {
	rows, err := s.files.ListByApplicationEnv(ctx, appID, env)
	if err != nil {
		return nil, err
	}
	return markCurrent(rows), nil
}

func (s *MachineFileService) ListForMachine(ctx context.Context, machineID int64) ([]*domain.MachineFile, error) {
	rows, err := s.files.ListByMachine(ctx, machineID)
	if err != nil {
		return nil, err
	}
	return markCurrent(rows), nil
}

// markCurrent flags the newest row per (machine, path) so the console can show
// history without implying that a superseded row describes the file on disk.
//
// It relies on rows arriving newest-first and covering every row of each group
// they touch, which both list queries guarantee — so the first sighting of a
// (machine, path) is the current one.
func markCurrent(rows []*domain.MachineFile) []*domain.MachineFile {
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		key := strconv.FormatInt(r.MachineID, 10) + "\x00" + r.Path
		if !seen[key] {
			seen[key] = true
			r.Current = true
		}
	}
	return rows
}

// ─── Verification ────────────────────────────────────────────────────────

// Verify re-reads one recorded file on its machine and stores the outcome.
func (s *MachineFileService) Verify(ctx context.Context, id int64) (*domain.MachineFile, error) {
	rec, err := s.files.GetByID(ctx, id)
	if err != nil {
		return nil, ErrMachineFileNotFound
	}
	s.verify(ctx, rec)
	if err := s.files.UpdateVerification(ctx, rec); err != nil {
		return nil, err
	}
	if current, err := s.files.IsCurrent(ctx, rec); err == nil {
		rec.Current = current
	}
	return rec, nil
}

// VerifyAllCurrent re-checks every file that is supposed to be on a machine
// right now, and returns how many are not in the state the platform recorded.
//
// This runs on a ticker rather than only behind the console's button because a
// file that vanishes is silent by nature: the application that needed it fails
// later, somewhere else, in a way that does not name the missing file.
func (s *MachineFileService) VerifyAllCurrent(ctx context.Context) (checked int, problems int, err error) {
	rows, err := s.files.ListCurrent(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, rec := range rows {
		s.verify(ctx, rec)
		if err := s.files.UpdateVerification(ctx, rec); err != nil {
			continue
		}
		checked++
		if rec.LastVerifyStatus != domain.FileVerifyOK {
			problems++
		}
	}
	return checked, problems, nil
}

// verify fills in rec's verification fields from the machine's current state.
func (s *MachineFileService) verify(ctx context.Context, rec *domain.MachineFile) {
	now := time.Now()
	rec.LastVerifiedAt = &now
	rec.LastVerifyError = ""
	rec.LastVerifySHA256 = ""

	m, err := s.machines.Get(ctx, rec.MachineID)
	if err != nil {
		rec.LastVerifyStatus = domain.FileVerifyUnreachable
		rec.LastVerifyError = "machine unavailable: " + err.Error()
		return
	}
	stat, err := runner.New(s.machines.ToMachineConfig(m)).StatFile(ctx, rec.Path)
	if err != nil {
		// The machine could not be asked, so nothing is known about the file.
		// Reporting this as "missing" would raise a false alarm every time a
		// node restarts.
		rec.LastVerifyStatus = domain.FileVerifyUnreachable
		rec.LastVerifyError = truncateForColumn(err.Error(), 512)
		return
	}
	switch {
	case !stat.Exists:
		rec.LastVerifyStatus = domain.FileVerifyMissing
	case stat.IsDir:
		rec.LastVerifyStatus = domain.FileVerifyChanged
		rec.LastVerifyError = "a directory now occupies this path"
	case rec.SHA256 != "" && stat.SHA256 == rec.SHA256:
		rec.LastVerifyStatus = domain.FileVerifyOK
		rec.LastVerifySHA256 = stat.SHA256
	default:
		rec.LastVerifyStatus = domain.FileVerifyChanged
		rec.LastVerifySHA256 = stat.SHA256
	}
}

func truncateForColumn(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// ─── Deploy-time check ───────────────────────────────────────────────────

// EnvFileCheck is one expected file's state on the machine being deployed to.
type EnvFileCheck struct {
	FileName string
	Path     string
	Status   domain.FileVerifyStatus
	Expected string
	Actual   string
	Detail   string
}

// OK reports whether the file is present with the expected contents.
func (c EnvFileCheck) OK() bool { return c.Status == domain.FileVerifyOK }

// CheckEnvFiles reports, for every file the platform has ever delivered to
// (app, env) on any machine, whether that file is present and unchanged on this
// particular machine.
//
// Checking against the environment rather than against this machine's own rows
// is the whole point: a machine added after the upload has no rows of its own,
// so a per-machine check would call it clean while its containers start without
// the credential. Because file contents are not stored, the platform cannot fix
// that automatically — but it can refuse to let it pass unnoticed.
func (s *MachineFileService) CheckEnvFiles(ctx context.Context, appID int64, env domain.Environment, machineID int64) ([]EnvFileCheck, error) {
	rows, err := s.files.ListByApplicationEnv(ctx, appID, env)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	m, err := s.machines.Get(ctx, machineID)
	if err != nil {
		return nil, err
	}

	// Rows arrive newest first, so the first sighting of a name is the most
	// recent upload of it anywhere in the environment — the version this
	// machine is expected to be carrying.
	expected := make(map[string]*domain.MachineFile)
	order := make([]string, 0, len(rows))
	for _, r := range rows {
		if _, seen := expected[r.FileName]; seen {
			continue
		}
		expected[r.FileName] = r
		order = append(order, r.FileName)
	}

	app, err := s.apps.GetByID(ctx, appID)
	if err != nil {
		return nil, err
	}
	dir, err := s.envFilesDir(m, app.Name, env)
	if err != nil {
		return nil, err
	}
	r := runner.New(s.machines.ToMachineConfig(m))

	out := make([]EnvFileCheck, 0, len(order))
	for _, name := range order {
		want := expected[name]
		target := path.Join(dir, name)
		check := EnvFileCheck{FileName: name, Path: target, Expected: want.SHA256}
		stat, err := r.StatFile(ctx, target)
		switch {
		case err != nil:
			check.Status = domain.FileVerifyUnreachable
			check.Detail = err.Error()
		case !stat.Exists:
			check.Status = domain.FileVerifyMissing
		case stat.SHA256 == want.SHA256:
			check.Status = domain.FileVerifyOK
			check.Actual = stat.SHA256
		default:
			check.Status = domain.FileVerifyChanged
			check.Actual = stat.SHA256
		}
		out = append(out, check)
	}
	return out, nil
}
