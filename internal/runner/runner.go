// Package runner provides a uniform interface for executing shell commands
// either locally or on a remote machine via SSH.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/agenda-v2/config"
)

// procGroupWaitDelay bounds how long Cmd.Wait blocks after the context is
// cancelled, so a lingering descendant holding stdout/stderr can never wedge
// the I/O-copy goroutine.
const procGroupWaitDelay = 5 * time.Second

// hardenProcGroup isolates a child command in its own process group and
// rewires context cancellation to SIGKILL the whole group rather than just
// the direct child, so subprocess helpers (git, ssh) don't linger as zombies
// under a Go binary running as PID 1.
func hardenProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = procGroupWaitDelay
}

// Runner executes commands in a given working directory.
type Runner interface {
	// RunCmd runs a binary with explicit args, writing stdout+stderr to buf.
	RunCmd(ctx context.Context, dir, name string, args []string, buf *bytes.Buffer) error
	// RunCmdEnv is RunCmd plus extra "KEY=VALUE" env vars (local: appended to
	// process env; ssh: prefixed to the remote command).
	RunCmdEnv(ctx context.Context, dir string, env []string, name string, args []string, buf *bytes.Buffer) error
	// RunShell runs a raw shell string via sh -c, writing stdout+stderr to buf.
	RunShell(ctx context.Context, dir, shellCmd string, buf *bytes.Buffer) error
}

// New returns a LocalRunner when machine is nil/local, otherwise an SSHRunner.
func New(machine *config.MachineConfig) Runner {
	if machine.IsLocal() {
		return &localRunner{}
	}
	return &sshRunner{machine: machine}
}

// ─── Local ───────────────────────────────────────────────────────────────

type localRunner struct{}

func (l *localRunner) RunCmd(ctx context.Context, dir, name string, args []string, buf *bytes.Buffer) error {
	return l.RunCmdEnv(ctx, dir, nil, name, args, buf)
}

func (l *localRunner) RunCmdEnv(ctx context.Context, dir string, env []string, name string, args []string, buf *bytes.Buffer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = buf
	cmd.Stderr = buf
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	hardenProcGroup(cmd)
	return cmd.Run()
}

func (l *localRunner) RunShell(ctx context.Context, dir, shellCmd string, buf *bytes.Buffer) error {
	return l.RunCmd(ctx, dir, "sh", []string{"-c", shellCmd}, buf)
}

// ─── SSH ────────────────────────────────────────────────────────────────

type sshRunner struct {
	machine *config.MachineConfig
}

func (s *sshRunner) RunCmd(ctx context.Context, dir, name string, args []string, buf *bytes.Buffer) error {
	return s.RunCmdEnv(ctx, dir, nil, name, args, buf)
}

func (s *sshRunner) RunCmdEnv(ctx context.Context, dir string, env []string, name string, args []string, buf *bytes.Buffer) error {
	parts := make([]string, 0, len(env)+len(args)+1)
	for _, e := range env {
		eq := strings.IndexByte(e, '=')
		if eq <= 0 {
			continue
		}
		parts = append(parts, e[:eq]+"="+shellQuote(e[eq+1:]))
	}
	parts = append(parts, shellQuote(name))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	remote := strings.Join(parts, " ")
	if dir != "" {
		remote = "cd " + shellQuote(dir) + " && " + remote
	}
	return s.runRemote(ctx, remote, buf)
}

func (s *sshRunner) RunShell(ctx context.Context, dir, shellCmd string, buf *bytes.Buffer) error {
	remote := shellCmd
	if dir != "" {
		remote = "cd " + shellQuote(dir) + " && " + shellCmd
	}
	return s.runRemote(ctx, remote, buf)
}

func (s *sshRunner) runRemote(ctx context.Context, remoteCmd string, buf *bytes.Buffer) error {
	sshArgs := s.sshArgs()
	sshArgs = append(sshArgs, remoteCmd)

	if s.machine.Password != "" {
		// sshpass -e reads the password from SSHPASS so it never appears in argv.
		args := append([]string{"-e", "ssh"}, sshArgs...)
		cmd := exec.CommandContext(ctx, "sshpass", args...)
		cmd.Env = append(os.Environ(), "SSHPASS="+s.machine.Password)
		cmd.Stdout = buf
		cmd.Stderr = buf
		hardenProcGroup(cmd)
		return cmd.Run()
	}

	cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	cmd.Stdout = buf
	cmd.Stderr = buf
	hardenProcGroup(cmd)
	return cmd.Run()
}

func (s *sshRunner) sshArgs() []string {
	m := s.machine
	args := []string{"-o", "StrictHostKeyChecking=accept-new"}
	if m.Password == "" {
		args = append(args, "-o", "BatchMode=yes")
	}
	if m.SSHKeyPath != "" {
		args = append(args, "-i", expandTilde(m.SSHKeyPath))
	}
	port := m.Port
	if port <= 0 {
		port = 22
	}
	args = append(args, "-p", fmt.Sprintf("%d", port))

	user := m.User
	if user == "" {
		args = append(args, m.Host)
	} else {
		args = append(args, user+"@"+m.Host)
	}
	return args
}

// shellQuote wraps s in single quotes and escapes any existing single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := exec.Command("sh", "-c", "echo ~").Output()
		return strings.TrimSpace(string(home)) + path[1:]
	}
	return path
}
