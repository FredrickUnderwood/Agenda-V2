package node

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestJobStoreLifecycle(t *testing.T) {
	s := NewJobStore(1024, time.Hour)
	s.Dispatch("j1", func(ctx context.Context, buf *bytes.Buffer) error {
		buf.WriteString("hello output")
		return nil
	})
	waitFor(t, func() bool {
		st, _ := s.Get("j1")
		return st.Status == StatusSuccess
	})
	st, ok := s.Get("j1")
	if !ok || st.Status != StatusSuccess || st.Output != "hello output" {
		t.Fatalf("job status = %+v ok=%v", st, ok)
	}
}

func TestJobStoreFailurePropagates(t *testing.T) {
	s := NewJobStore(1024, time.Hour)
	s.Dispatch("j2", func(ctx context.Context, buf *bytes.Buffer) error {
		return errors.New("boom")
	})
	waitFor(t, func() bool {
		st, _ := s.Get("j2")
		return st.Status == StatusFailed
	})
	st, _ := s.Get("j2")
	if st.Error != "boom" {
		t.Fatalf("error = %q, want boom", st.Error)
	}
}

func TestJobStoreDispatchIdempotent(t *testing.T) {
	s := NewJobStore(1024, time.Hour)
	var runs int32
	block := make(chan struct{})
	fn := func(ctx context.Context, buf *bytes.Buffer) error {
		atomic.AddInt32(&runs, 1)
		<-block
		return nil
	}
	s.Dispatch("dup", fn)
	s.Dispatch("dup", fn) // same id → must not start a second run
	close(block)
	waitFor(t, func() bool {
		st, _ := s.Get("dup")
		return st.Status == StatusSuccess
	})
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("run count = %d, want 1 (dispatch must be idempotent)", got)
	}
}

func TestJobStoreDeleteCancels(t *testing.T) {
	s := NewJobStore(1024, time.Hour)
	cancelled := make(chan struct{})
	s.Dispatch("cancelme", func(ctx context.Context, buf *bytes.Buffer) error {
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	})
	if !s.Delete("cancelme") {
		t.Fatal("Delete returned false for existing job")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("Delete did not cancel the running job's context")
	}
	if _, ok := s.Get("cancelme"); ok {
		t.Fatal("job still present after Delete")
	}
}

func TestJobStoreOutputCapped(t *testing.T) {
	s := NewJobStore(10, time.Hour)
	s.Dispatch("big", func(ctx context.Context, buf *bytes.Buffer) error {
		buf.WriteString("0123456789ABCDEFGHIJ") // 20 bytes, cap 10
		return nil
	})
	waitFor(t, func() bool {
		st, _ := s.Get("big")
		return st.Status == StatusSuccess
	})
	st, _ := s.Get("big")
	if len(st.Output) <= 10 || st.Output[len(st.Output)-10:] != "ABCDEFGHIJ" {
		t.Fatalf("capped output = %q, want tail ABCDEFGHIJ with marker", st.Output)
	}
}

func TestSplitInstancePrefix(t *testing.T) {
	cases := []struct {
		path           string
		instance, rest string
		ok             bool
	}{
		{"/i/pay-prod/api/v1/x", "pay-prod", "/api/v1/x", true},
		{"/i/pay-prod", "pay-prod", "/", true},
		{"/i/pay-prod/", "pay-prod", "/", true},
		{"/other/thing", "", "", false},
		{"/i/", "", "", false},
	}
	for _, tc := range cases {
		gotInst, gotRest, gotOK := splitInstancePrefix(tc.path)
		if gotInst != tc.instance || gotRest != tc.rest || gotOK != tc.ok {
			t.Errorf("splitInstancePrefix(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.path, gotInst, gotRest, gotOK, tc.instance, tc.rest, tc.ok)
		}
	}
}
