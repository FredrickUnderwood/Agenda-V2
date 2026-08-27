package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/filestore"
)

// ─── Local ───────────────────────────────────────────────────────────────

func (l *localRunner) PutFile(ctx context.Context, path string, src io.Reader, mode string, overwrite bool) (contract.FileStat, error) {
	clean, err := filestore.ValidatePath(path, nil)
	if err != nil {
		return contract.FileStat{}, err
	}
	m, err := filestore.ParseMode(mode)
	if err != nil {
		return contract.FileStat{}, err
	}
	return filestore.WriteAt(clean, m, overwrite, src, 0)
}

func (l *localRunner) StatFile(ctx context.Context, path string) (contract.FileStat, error) {
	clean, err := filestore.ValidatePath(path, nil)
	if err != nil {
		return contract.FileStat{}, err
	}
	return filestore.StatAt(clean)
}

// ─── SSH ────────────────────────────────────────────────────────────────

// sshPutExistsCode is the exit status the remote script uses for "already
// there, and you didn't ask me to replace it". It has to be distinguishable
// from a generic failure, since the caller turns it into a 409 rather than a
// 500.
const sshPutExistsCode = 3

func (s *sshRunner) PutFile(ctx context.Context, path string, src io.Reader, mode string, overwrite bool) (contract.FileStat, error) {
	clean, err := filestore.ValidatePath(path, nil)
	if err != nil {
		return contract.FileStat{}, err
	}
	m, err := filestore.ParseMode(mode)
	if err != nil {
		return contract.FileStat{}, err
	}
	dir := parentDir(clean)

	// The bytes travel on stdin into a temporary file beside the destination
	// and are renamed into place at the end, so an app reading this path never
	// observes a partially transferred file — the same guarantee the local and
	// node writers give.
	var script strings.Builder
	script.WriteString("set -e\n")
	if !overwrite {
		script.WriteString("if [ -e " + shellQuote(clean) + " ]; then echo 'file already exists' >&2; exit " +
			strconv.Itoa(sshPutExistsCode) + "; fi\n")
	}
	script.WriteString("mkdir -p " + shellQuote(dir) + "\n")
	script.WriteString("tmp=" + shellQuote(dir) + "/.agenda-upload.$$\n")
	script.WriteString("trap 'rm -f \"$tmp\"' EXIT\n")
	script.WriteString("cat > \"$tmp\"\n")
	script.WriteString("chmod " + filestore.FormatMode(m) + " \"$tmp\"\n")
	script.WriteString("mv -f \"$tmp\" " + shellQuote(clean) + "\n")

	var buf bytes.Buffer
	if err := s.runRemoteStdin(ctx, script.String(), src, &buf); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == sshPutExistsCode {
			return contract.FileStat{}, filestore.ErrExists
		}
		return contract.FileStat{}, fmt.Errorf("put file %s: %s", clean, cmdFailure(&buf, err))
	}
	// Hash what actually landed rather than what was sent: the checksum has to
	// describe the file on the machine for a later verification to mean
	// anything.
	return s.StatFile(ctx, clean)
}

func (s *sshRunner) StatFile(ctx context.Context, path string) (contract.FileStat, error) {
	clean, err := filestore.ValidatePath(path, nil)
	if err != nil {
		return contract.FileStat{}, err
	}
	q := shellQuote(clean)
	// `stat -c` is GNU; the -f form is the BSD/macOS spelling. Deploy targets
	// are Linux, but the fallback costs one line and turns an obscure parse
	// failure into a working answer on a developer's own machine.
	script := "if [ ! -e " + q + " ]; then echo missing; exit 0; fi\n" +
		"if [ -d " + q + " ]; then echo dir; exit 0; fi\n" +
		"m=$(stat -c '%s %a %Y' " + q + " 2>/dev/null || stat -f '%z %Lp %m' " + q + ")\n" +
		"h=$(sha256sum " + q + " 2>/dev/null | cut -d' ' -f1 || shasum -a 256 " + q + " | cut -d' ' -f1)\n" +
		"echo \"file $m $h\"\n"

	var buf bytes.Buffer
	if err := s.runRemote(ctx, script, &buf); err != nil {
		return contract.FileStat{}, fmt.Errorf("stat file %s: %s", clean, cmdFailure(&buf, err))
	}
	return parseStatOutput(buf.String())
}

// parseStatOutput reads the single line the remote stat script prints:
// "missing", "dir", or "file <size> <octal mode> <mtime> <sha256>".
func parseStatOutput(out string) (contract.FileStat, error) {
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
		}
	}
	fields := strings.Fields(line)
	switch {
	case len(fields) == 0:
		return contract.FileStat{}, errors.New("stat: empty response from remote")
	case fields[0] == "missing":
		return contract.FileStat{Exists: false}, nil
	case fields[0] == "dir":
		return contract.FileStat{Exists: true, IsDir: true}, nil
	case fields[0] == "file" && len(fields) == 5:
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return contract.FileStat{}, fmt.Errorf("stat: bad size %q", fields[1])
		}
		modTime, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return contract.FileStat{}, fmt.Errorf("stat: bad mtime %q", fields[3])
		}
		return contract.FileStat{
			Exists:      true,
			Size:        size,
			Mode:        normalizeOctal(fields[2]),
			ModTimeUnix: modTime,
			SHA256:      fields[4],
		}, nil
	default:
		return contract.FileStat{}, fmt.Errorf("stat: unrecognized response %q", line)
	}
}

// normalizeOctal pads `stat -c %a`'s output ("600") to the leading-zero form
// the rest of the system uses ("0600"), so a mode compared across backends
// compares equal.
func normalizeOctal(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "0") {
		return s
	}
	return "0" + s
}

// parentDir is filepath.Dir for remote (always POSIX) paths — the control plane
// may run on any OS, so the host's separator must not leak into a command that
// executes on Linux.
func parentDir(path string) string {
	i := strings.LastIndex(path, "/")
	switch {
	case i < 0:
		return "."
	case i == 0:
		return "/"
	default:
		return path[:i]
	}
}

// cmdFailure prefers the command's own output over the bare exit status, which
// on its own says nothing about what went wrong.
func cmdFailure(buf *bytes.Buffer, err error) string {
	if s := strings.TrimSpace(buf.String()); s != "" {
		return s
	}
	return err.Error()
}
