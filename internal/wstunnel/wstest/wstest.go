// Package wstest is minimal WebSocket scaffolding for tests: a backend handler
// that completes a real RFC 6455 handshake, and a client that performs one.
//
// It lives in a normal (non _test) package because more than one package's
// tests need it — the gateway's proxy tests, agenda-node's relay tests, and the
// two-hop gateway→node→app test that exercises both at once.
//
// Deliberately no frame codec. After the 101, both proxies are byte tunnels
// that never look at the payload, so tests exchange raw bytes: that is exactly
// the contract under test, and a frame encoder would only add a second
// implementation of something neither proxy has an opinion about.
package wstest

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// acceptGUID is the RFC 6455 handshake constant.
const acceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// AcceptKey computes Sec-WebSocket-Accept from a client's Sec-WebSocket-Key.
func AcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + acceptGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// Handler is a WebSocket backend for tests. With no fields set it completes the
// handshake and echoes every byte it receives.
type Handler struct {
	// RejectStatus, when non-zero, refuses the upgrade with that status code
	// instead of switching protocols — the "backend declined" path.
	RejectStatus int
	// Subprotocol is echoed back as Sec-WebSocket-Protocol.
	Subprotocol string
	// OnRequest observes the handshake request as the backend received it, for
	// asserting on path, query and forwarded headers.
	OnRequest func(*http.Request)
	// Serve replaces the default echo loop. It owns conn and must close it.
	Serve func(conn net.Conn, brw *bufio.ReadWriter)

	mu    sync.Mutex
	conns []net.Conn
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.OnRequest != nil {
		h.OnRequest(r)
	}
	if h.RejectStatus != 0 {
		http.Error(w, "upgrade refused", h.RejectStatus)
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "not a websocket request", http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijacker", http.StatusInternalServerError)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	h.mu.Lock()
	h.conns = append(h.conns, conn)
	h.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	sb.WriteString("Upgrade: websocket\r\n")
	sb.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&sb, "Sec-WebSocket-Accept: %s\r\n", AcceptKey(r.Header.Get("Sec-WebSocket-Key")))
	if h.Subprotocol != "" {
		fmt.Fprintf(&sb, "Sec-WebSocket-Protocol: %s\r\n", h.Subprotocol)
	}
	sb.WriteString("\r\n")
	if _, err := brw.WriteString(sb.String()); err != nil {
		conn.Close()
		return
	}
	if err := brw.Flush(); err != nil {
		conn.Close()
		return
	}

	if h.Serve != nil {
		h.Serve(conn, brw)
		return
	}
	defer conn.Close()
	_, _ = io.Copy(conn, conn)
}

// CloseAll closes every connection the handler accepted, simulating the backend
// (or its container) going away underneath a live tunnel.
func (h *Handler) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, conn := range h.conns {
		_ = conn.Close()
	}
}

// Client is one end of an established tunnel.
type Client struct {
	Conn     net.Conn
	Response *http.Response
	br       *bufio.Reader
}

// DialOptions tunes the client handshake.
type DialOptions struct {
	Header      http.Header
	TLS         bool
	DialTimeout time.Duration
}

// Dial performs a WebSocket handshake against rawURL ("http://host/path" or
// "ws://host/path") and returns the established tunnel.
//
// A non-101 response is returned as an error together with the response, so a
// test can assert on the rejection status the gateway produced.
func Dial(rawURL string, opts DialOptions) (*Client, *http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, err
	}
	useTLS := opts.TLS || u.Scheme == "https" || u.Scheme == "wss"
	host := u.Host
	if u.Port() == "" {
		if useTLS {
			host = net.JoinHostPort(host, "443")
		} else {
			host = net.JoinHostPort(host, "80")
		}
	}
	timeout := opts.DialTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	var conn net.Conn
	dialer := &net.Dialer{Timeout: timeout}
	if useTLS {
		// Tests terminate TLS with a self-signed httptest certificate.
		conn, err = tls.DialWithDialer(dialer, "tcp", host, &tls.Config{InsecureSkipVerify: true})
	} else {
		conn, err = dialer.Dial("tcp", host)
	}
	if err != nil {
		return nil, nil, err
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	path := u.RequestURI()
	var req strings.Builder
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&req, "Host: %s\r\n", u.Host)
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&req, "Sec-WebSocket-Key: %s\r\n", key)
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	for name, values := range opts.Header {
		for _, value := range values {
			fmt.Fprintf(&req, "%s: %s\r\n", name, value)
		}
	}
	req.WriteString("\r\n")
	if _, err := conn.Write([]byte(req.String())); err != nil {
		conn.Close()
		return nil, nil, err
	}

	br := bufio.NewReader(conn)
	// The handshake response has no body (1xx), so br keeps whatever tunnel
	// bytes arrived with it — the client must keep reading through br, not the
	// bare conn.
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, resp, fmt.Errorf("websocket handshake failed: %s", resp.Status)
	}
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != AcceptKey(key) {
		conn.Close()
		return nil, resp, errors.New("websocket handshake accept key mismatch")
	}
	return &Client{Conn: conn, Response: resp, br: br}, resp, nil
}

// Send writes raw payload bytes into the tunnel.
func (c *Client) Send(payload string) error {
	_, err := c.Conn.Write([]byte(payload))
	return err
}

// Receive reads exactly n payload bytes back out of the tunnel.
func (c *Client) Receive(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// Echo sends payload and reads back the same number of bytes.
func (c *Client) Echo(payload string) (string, error) {
	if err := c.Send(payload); err != nil {
		return "", err
	}
	return c.Receive(len(payload))
}

// SetDeadline bounds a test's reads so a hung tunnel fails fast.
func (c *Client) SetDeadline(t time.Time) error { return c.Conn.SetDeadline(t) }

func (c *Client) Close() error { return c.Conn.Close() }
