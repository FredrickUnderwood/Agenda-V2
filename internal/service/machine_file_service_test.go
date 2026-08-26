package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/filestore"
	"github.com/FredrickUnderwood/agenda-v2/internal/secret"
)

// The console shows the full upload history, so exactly one row per
// (machine, path) must be flagged as describing the file on disk — otherwise a
// superseded row's checksum reads as the current state.
func TestMarkCurrent_FlagsOnlyTheNewestRowPerMachineAndPath(t *testing.T) {
	// Newest first, matching the order both list queries return.
	rows := []*domain.MachineFile{
		{ID: 5, MachineID: 1, Path: "/run/app/prod/.files/key.p8"},
		{ID: 4, MachineID: 2, Path: "/run/app/prod/.files/key.p8"},
		{ID: 3, MachineID: 1, Path: "/run/app/prod/.files/other.pem"},
		{ID: 2, MachineID: 1, Path: "/run/app/prod/.files/key.p8"},
		{ID: 1, MachineID: 2, Path: "/run/app/prod/.files/key.p8"},
	}
	markCurrent(rows)

	want := map[int64]bool{5: true, 4: true, 3: true, 2: false, 1: false}
	for _, r := range rows {
		if r.Current != want[r.ID] {
			t.Errorf("row %d: Current = %v, want %v", r.ID, r.Current, want[r.ID])
		}
	}
}

// The same file name on two different machines is two independent records, not
// one superseding the other.
func TestMarkCurrent_SameNameOnDifferentMachinesAreBothCurrent(t *testing.T) {
	rows := []*domain.MachineFile{
		{ID: 2, MachineID: 7, Path: "/files/key.p8"},
		{ID: 1, MachineID: 8, Path: "/files/key.p8"},
	}
	markCurrent(rows)
	for _, r := range rows {
		if !r.Current {
			t.Errorf("row %d on machine %d should be current", r.ID, r.MachineID)
		}
	}
}

func TestFileNamePattern(t *testing.T) {
	valid := []string{"key.p8", "apple-key_1.pem", "a", "0cert.crt"}
	for _, name := range valid {
		if !fileNamePattern.MatchString(name) {
			t.Errorf("%q should be a valid file name", name)
		}
	}
	// A name becomes a path component on every machine and a filename inside
	// every container, so anything that could traverse or confuse either is out.
	invalid := []string{"", ".", "..", "../escape", "dir/key.p8", "-rf", "key p8", "key;rm", ".hidden"}
	for _, name := range invalid {
		if fileNamePattern.MatchString(name) {
			t.Errorf("%q should be rejected as a file name", name)
		}
	}
}

// A machine upload outside the workspace root must be refused rather than
// attempted. A containerized agenda-node with no file_roots would happily write
// such a path inside its own container: the upload reports success, verifies as
// OK, and disappears on the next node restart.
func TestUploadToMachine_RejectsPathOutsideWorkspaceRoot(t *testing.T) {
	svc := &MachineFileService{machines: machineServiceWith(&domain.Machine{
		ID: 1, Name: "node-1", WorkspaceRoot: "/root/.agenda-v2/workspaces",
	})}

	_, err := svc.UploadToMachine(context.Background(), Principal{Username: "someone"}, 1,
		"/tmp/key.p8", "0600", false, failingOpener)
	if !errors.Is(err, filestore.ErrOutsideRoots) {
		t.Fatalf("err = %v, want ErrOutsideRoots", err)
	}
	// The message has to name the root, since the operator cannot see it from
	// the error alone otherwise.
	if !strings.Contains(err.Error(), "/root/.agenda-v2/workspaces") {
		t.Errorf("error %q does not name the workspace root", err)
	}
}

func TestUploadToMachine_RejectsMachineWithNoWorkspaceRoot(t *testing.T) {
	svc := &MachineFileService{machines: machineServiceWith(&domain.Machine{ID: 1, Name: "node-1"})}

	_, err := svc.UploadToMachine(context.Background(), Principal{Username: "someone"}, 1,
		"/anywhere/key.p8", "0600", false, failingOpener)
	if !errors.Is(err, ErrNoWorkspaceRoot) {
		t.Fatalf("err = %v, want ErrNoWorkspaceRoot", err)
	}
}

// A relative or unclean path is rejected before any machine lookup happens.
func TestUploadToMachine_RejectsUncleanPath(t *testing.T) {
	svc := &MachineFileService{machines: machineServiceWith(&domain.Machine{ID: 1, WorkspaceRoot: "/srv"})}
	for _, p := range []string{"relative/key.p8", "/srv/../etc/passwd", "/srv/x/"} {
		if _, err := svc.UploadToMachine(context.Background(), Principal{}, 1, p, "0600", false, failingOpener); err == nil {
			t.Errorf("path %q was accepted", p)
		}
	}
}

// failingOpener makes it obvious if a rejected upload ever reaches the transfer.
func failingOpener() (io.ReadCloser, error) {
	return nil, errors.New("the content should never be opened for a rejected upload")
}

func machineServiceWith(m *domain.Machine) *MachineService {
	return &MachineService{machines: singleMachineRepo{m}, box: secret.NewBox("")}
}

// singleMachineRepo is the slice of MachineRepo these tests exercise; the rest
// panics rather than silently returning zero values.
type singleMachineRepo struct{ m *domain.Machine }

func (r singleMachineRepo) GetByID(ctx context.Context, id int64) (*domain.Machine, error) {
	if r.m == nil || r.m.ID != id {
		return nil, errors.New("machine not found")
	}
	copy := *r.m
	return &copy, nil
}

func (r singleMachineRepo) Create(context.Context, *domain.Machine) error { panic("unused") }
func (r singleMachineRepo) GetByName(context.Context, string) (*domain.Machine, error) {
	panic("unused")
}
func (r singleMachineRepo) List(context.Context) ([]*domain.Machine, error) { panic("unused") }
func (r singleMachineRepo) Update(context.Context, *domain.Machine) error   { panic("unused") }
func (r singleMachineRepo) Delete(context.Context, int64) error             { panic("unused") }
func (r singleMachineRepo) UpdateHeartbeat(context.Context, int64, string, time.Time) error {
	panic("unused")
}
