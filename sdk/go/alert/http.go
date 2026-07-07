package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// postJSON marshals body, POSTs it to url, and returns an error unless the
// response status is 2xx. On failure the error includes a truncated snippet
// of the response body — matches internal/nodeproxy/client.go's style.
func postJSON(ctx context.Context, url string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return errors.New("alert: send failed: " + resp.Status + ": " + strings.TrimSpace(string(msg)))
	}
	return nil
}
