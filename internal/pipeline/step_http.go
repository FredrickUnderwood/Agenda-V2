package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/agenda-v2/internal/domain"
)

// HTTPRequestStep performs the API-deploy HTTP call, capturing status+body
// into the step output and failing on unexpected status codes.
type HTTPRequestStep struct {
	Cfg *domain.APIDeployConfig
	// httpClient is overridable for tests; zero value falls back to a timed default.
	httpClient *http.Client
}

func (s *HTTPRequestStep) Execute(ctx context.Context, rc *RunContext) error {
	cfg := s.Cfg
	if cfg == nil {
		return errors.New("nil api config")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if cfg.TimeoutSeconds <= 0 {
		timeout = 30 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	method := strings.ToUpper(cfg.Method)
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(reqCtx, method, cfg.URL, bytes.NewBufferString(cfg.Body))
	if err != nil {
		return err
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	if cfg.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_, _ = fmt.Fprintf(rc.Output, "HTTP %d\n%s", resp.StatusCode, string(bodyBytes))

	expected := cfg.ExpectedStatus
	ok := (expected > 0 && resp.StatusCode == expected) ||
		(expected == 0 && resp.StatusCode >= 200 && resp.StatusCode < 300)
	if !ok {
		return errors.New("unexpected status " + strconv.Itoa(resp.StatusCode))
	}
	return nil
}
