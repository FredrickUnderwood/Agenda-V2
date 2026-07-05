package runner_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/node"
	"github.com/FredrickUnderwood/agenda-v2/internal/runner"
)

// newTestNode spins up a real agenda-node management server backed by an
// in-process JobStore, so the agentRunner exercises the true dispatch+poll wire
// path end to end.
func newTestNode(t *testing.T, token string) (*config.MachineConfig, func()) {
	t.Helper()
	jobs := node.NewJobStore(65536, time.Hour)
	srv := node.NewServer(token, jobs, node.NewProxyRegistry())
	ts := httptest.NewServer(srv.Handler())
	mc := &config.MachineConfig{
		Mode:              "agent",
		AgentBaseURL:      ts.URL,
		AgentToken:        token,
		AgentPollInterval: 10 * time.Millisecond,
	}
	return mc, ts.Close
}

func TestAgentRunnerRunShellSuccess(t *testing.T) {
	mc, closeFn := newTestNode(t, "tok")
	defer closeFn()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.New(mc).RunShell(ctx, "", "echo agent-hello", &buf); err != nil {
		t.Fatalf("RunShell: %v", err)
	}
	if !strings.Contains(buf.String(), "agent-hello") {
		t.Fatalf("output = %q, want it to contain agent-hello", buf.String())
	}
}

func TestAgentRunnerRunCmdFailurePropagates(t *testing.T) {
	mc, closeFn := newTestNode(t, "tok")
	defer closeFn()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// `false` exits non-zero → the job fails and the runner returns an error.
	err := runner.New(mc).RunCmd(ctx, "", "false", nil, &buf)
	if err == nil {
		t.Fatal("expected error from failing command")
	}
}

func TestAgentRunnerBadTokenRejected(t *testing.T) {
	mc, closeFn := newTestNode(t, "right-token")
	defer closeFn()
	mc.AgentToken = "wrong-token"

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.New(mc).RunShell(ctx, "", "echo x", &buf); err == nil {
		t.Fatal("expected dispatch to be rejected with a bad token")
	}
}

func TestAgentRunnerContextCancel(t *testing.T) {
	mc, closeFn := newTestNode(t, "tok")
	defer closeFn()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	// A command that outlives the ctx → run returns the ctx error, not success.
	err := runner.New(mc).RunShell(ctx, "", "sleep 5", &buf)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
}
