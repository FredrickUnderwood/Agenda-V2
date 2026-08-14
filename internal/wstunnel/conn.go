package wstunnel

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// IdleConn wraps a hijacked tunnel connection with a rolling deadline, giving
// the tunnel a real idle timeout rather than a maximum lifetime.
//
// Why this works: after the handshake, ReverseProxy copies bytes in both
// directions through the connection returned by Hijack — client→backend shows
// up here as a Read, backend→client as a Write. Refreshing the deadline on
// both means the tunnel only expires when NOTHING has crossed it in either
// direction for the idle window, which is what an operator means by "idle".
// When it does expire the pending Read fails with os.ErrDeadlineExceeded, both
// copy loops unwind, and the connection is closed.
type IdleConn struct {
	net.Conn
	idle     time.Duration
	timedOut atomic.Bool
}

// NewIdleConn returns c wrapped with an idle timeout. A non-positive idle
// returns c unchanged, so "no idle timeout" costs nothing per byte.
func NewIdleConn(c net.Conn, idle time.Duration) net.Conn {
	if idle <= 0 {
		return c
	}
	ic := &IdleConn{Conn: c, idle: idle}
	ic.touch()
	return ic
}

func (c *IdleConn) Read(b []byte) (int, error) {
	c.touch()
	n, err := c.Conn.Read(b)
	c.noteTimeout(err)
	return n, err
}

func (c *IdleConn) Write(b []byte) (int, error) {
	c.touch()
	n, err := c.Conn.Write(b)
	c.noteTimeout(err)
	return n, err
}

// TimedOut reports whether this connection ended because the idle deadline
// fired, so the caller can attribute the disconnect instead of guessing.
func (c *IdleConn) TimedOut() bool { return c.timedOut.Load() }

func (c *IdleConn) touch() {
	// SetDeadline is safe to call while a Read/Write is in flight on another
	// goroutine, which is exactly the case here (two copy loops).
	_ = c.Conn.SetDeadline(time.Now().Add(c.idle))
}

func (c *IdleConn) noteTimeout(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		c.timedOut.Store(true)
	}
}

// HijackHook wraps an http.ResponseWriter to intercept the moment the proxy
// hijacks the connection. For an upgrade that moment is significant: it happens
// only after the backend answered 101, so it is the point where the handshake
// is known to have succeeded and the raw connection first becomes available to
// wrap and register.
//
// Flush is forwarded so a non-upgraded response through the same writer still
// streams (SSE/chunked).
type HijackHook struct {
	http.ResponseWriter

	// OnHijack is called with the raw connection and returns the connection the
	// proxy should actually use — the place to layer on an idle timeout. It may
	// return conn unchanged. Never nil-checked by callers: set it or leave the
	// hook's zero value, which passes the connection through.
	OnHijack func(net.Conn) net.Conn

	hijacked atomic.Bool
}

// Hijacked reports whether the connection was taken over, i.e. whether the
// upgrade handshake completed.
func (h *HijackHook) Hijacked() bool { return h.hijacked.Load() }

func (h *HijackHook) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := h.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return conn, brw, err
	}
	h.hijacked.Store(true)
	if h.OnHijack != nil {
		conn = h.OnHijack(conn)
	}
	return conn, brw, nil
}

func (h *HijackHook) Flush() {
	if f, ok := h.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
