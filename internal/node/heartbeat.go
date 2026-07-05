package node

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
	"go.uber.org/zap"
)

// Version is the node build version reported in heartbeats and /v1/health.
const Version = "0.1.0"

// Heartbeat periodically POSTs the node's liveness to the control plane so the
// machine's online status can be derived from last_seen. It stops when ctx is
// cancelled. A failed heartbeat is logged and retried on the next tick — it is
// best-effort and never fatal.
type Heartbeat struct {
	centralBaseURL string
	machineID      int64
	token          string
	interval       time.Duration
	client         *http.Client
}

func NewHeartbeat(centralBaseURL string, machineID int64, token string, interval time.Duration) *Heartbeat {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &Heartbeat{
		centralBaseURL: strings.TrimRight(centralBaseURL, "/"),
		machineID:      machineID,
		token:          token,
		interval:       interval,
		client:         &http.Client{Timeout: 10 * time.Second},
	}
}

// Start launches the heartbeat loop in a goroutine. It sends one immediately so
// the control plane sees the node come online without waiting a full interval.
func (h *Heartbeat) Start(ctx context.Context) {
	if h.centralBaseURL == "" {
		log.Warn(ctx, "central_base_url not set; heartbeats disabled")
		return
	}
	go func() {
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()
		h.send(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.send(ctx)
			}
		}
	}()
}

func (h *Heartbeat) send(ctx context.Context) {
	url := h.centralBaseURL + "/api/v1/machines/" + strconv.FormatInt(h.machineID, 10) + "/heartbeat"
	body, _ := sonic.Marshal(contract.NodeHeartbeatRequest{Version: Version})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Warn(ctx, "build heartbeat request failed", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(contract.HeaderNodeToken, h.token)
	resp, err := h.client.Do(req)
	if err != nil {
		log.Warn(ctx, "heartbeat failed", zap.Error(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Warn(ctx, "heartbeat rejected", zap.Int("status", resp.StatusCode))
	}
}
