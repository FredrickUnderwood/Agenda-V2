package gatewayclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/config"
	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/bytedance/sonic"
	"io"
)

func TestRouteLifecycleRequiresGatewayAcknowledgment(t *testing.T) {
	for _, code := range []int{http.StatusNoContent, http.StatusNotFound, http.StatusInternalServerError, http.StatusOK} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(serviceTokenHeader) != "test-token" {
					t.Error("service token missing")
				}
				if r.Method != http.MethodDelete || r.URL.Path != "/-/routes/old-test" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				var owner contract.RouteOwner
				if err := sonic.Unmarshal(raw, &owner); err != nil || owner.ApplicationID != 11 || owner.Env != "test" {
					t.Errorf("wrong owner: %+v, %v", owner, err)
				}
				w.WriteHeader(code)
			}))
			defer server.Close()
			client := NewClient(config.GatewayConfig{BaseURL: server.URL, ServiceToken: "test-token"})
			err := client.DeleteRoute(context.Background(), "old-test", contract.RouteOwner{ApplicationID: 11, Env: "test"})
			if (err == nil) != (code == http.StatusNoContent) {
				t.Fatalf("status %d: %v", code, err)
			}
		})
	}
}
