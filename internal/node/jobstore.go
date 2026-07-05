package node

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

// Job execution states reported to the control plane.
const (
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

type job struct {
	mu     sync.Mutex
	status string
	buf    bytes.Buffer // written only by the run goroutine; read only once terminal
	err    string
	doneAt time.Time
	cancel context.CancelFunc
}

// Output is populated only when the job is terminal (success/failed); while
// running it is empty. Streaming partial output during a run is a deferred
// enhancement — returning it only at the end keeps the job buffer free of
// concurrent read/write (the run goroutine is the sole writer and has finished
// before any terminal read).
//
// JobStore is agenda-node's in-memory task table. Jobs are not persisted — a
// node restart drops them, and the control plane treats a 404 on a job it
// dispatched as a lost task (step failure), the same outcome as a dropped SSH
// session. A background sweeper GCs finished jobs after retention.
type JobStore struct {
	mu             sync.Mutex
	jobs           map[string]*job
	maxOutputBytes int
	retention      time.Duration
	maxJobDuration time.Duration
}

func NewJobStore(maxOutputBytes int, retention time.Duration) *JobStore {
	if maxOutputBytes <= 0 {
		maxOutputBytes = 65536
	}
	if retention <= 0 {
		retention = time.Hour
	}
	return &JobStore{
		jobs:           make(map[string]*job),
		maxOutputBytes: maxOutputBytes,
		retention:      retention,
		// A single job (e.g. docker compose up --build pulling a large image)
		// can run for minutes; cap it generously so a wedged command still frees
		// eventually even if the control plane never sends DELETE.
		maxJobDuration: 30 * time.Minute,
	}
}

// Dispatch is idempotent: dispatching an existing job_id returns without
// starting a second command, so a retried POST never double-runs a deploy. run
// receives a context cancelled by DELETE (orphan reclamation) or maxJobDuration.
func (s *JobStore) Dispatch(id string, run func(ctx context.Context, buf *bytes.Buffer) error) {
	s.mu.Lock()
	if _, exists := s.jobs[id]; exists {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.maxJobDuration)
	j := &job{status: StatusRunning, cancel: cancel}
	s.jobs[id] = j
	s.mu.Unlock()

	go func() {
		defer cancel()
		err := run(ctx, &j.buf)
		j.mu.Lock()
		j.doneAt = time.Now()
		if err != nil {
			j.status, j.err = StatusFailed, err.Error()
		} else {
			j.status = StatusSuccess
		}
		j.mu.Unlock()
	}()
}

// Get returns the job's current status; ok is false if unknown. Output is
// included only when terminal (the run goroutine has finished writing buf).
func (s *JobStore) Get(id string) (contract.NodeJobStatus, bool) {
	s.mu.Lock()
	j, ok := s.jobs[id]
	s.mu.Unlock()
	if !ok {
		return contract.NodeJobStatus{}, false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	st := contract.NodeJobStatus{Status: j.status, Error: j.err}
	if j.status != StatusRunning {
		st.Output = capTail(j.buf.String(), s.maxOutputBytes)
	}
	return st, true
}

// Delete cancels a still-running job and removes it. Returns false if unknown.
func (s *JobStore) Delete(id string) bool {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if ok {
		delete(s.jobs, id)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	if j.cancel != nil {
		j.cancel()
	}
	return true
}

// StartGC runs a sweeper that removes finished jobs older than retention until
// ctx is cancelled.
func (s *JobStore) StartGC(ctx context.Context) {
	interval := s.retention / 2
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweep()
			}
		}
	}()
}

func (s *JobStore) sweep() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, j := range s.jobs {
		j.mu.Lock()
		expired := !j.doneAt.IsZero() && now.Sub(j.doneAt) > s.retention
		j.mu.Unlock()
		if expired {
			delete(s.jobs, id)
		}
	}
}

// capTail returns the last max bytes of s (the tail, where a failing command's
// error typically is), prefixed with a truncation marker when it clips.
func capTail(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return "...[truncated]...\n" + s[len(s)-max:]
}
