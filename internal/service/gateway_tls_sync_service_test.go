package service

import (
	"context"
	"errors"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

type fakeSettingReader map[string]string

func (f fakeSettingReader) GetByPrefix(prefix string) map[string]string {
	out := make(map[string]string)
	for k, v := range f {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out[k] = v
		}
	}
	return out
}

type capturePusher struct {
	got    contract.UpdateTLSConfigRequest
	called bool
}

func (c *capturePusher) PutTLSConfig(_ context.Context, req contract.UpdateTLSConfigRequest) error {
	c.got = req
	c.called = true
	return nil
}

func TestSyncSkipsWhenCredentialsMissing(t *testing.T) {
	// Only email set, no aliyun keys -> not configured, no push.
	settings := fakeSettingReader{SettingKeyGatewayTLSACMEEmail: "ops@example.com"}
	pusher := &capturePusher{}
	svc := NewGatewayTLSSyncService(settings, pusher)

	err := svc.Sync(context.Background())
	if !errors.Is(err, ErrGatewayTLSNotConfigured) {
		t.Fatalf("Sync() err = %v, want ErrGatewayTLSNotConfigured", err)
	}
	if pusher.called {
		t.Fatal("pusher should not be called when credentials are missing")
	}
}

func TestSyncPushesWithDefaults(t *testing.T) {
	settings := fakeSettingReader{
		SettingKeyGatewayTLSACMEEmail:      "ops@example.com",
		SettingKeyGatewayTLSAliyunAKID:     "ak",
		SettingKeyGatewayTLSAliyunAKSecret: "sk",
		SettingKeyGatewayTLSStaticDomains:  "admin.example.com, www.example.com",
		// CA and DNS provider intentionally omitted -> defaults apply.
	}
	pusher := &capturePusher{}
	svc := NewGatewayTLSSyncService(settings, pusher)

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() err = %v", err)
	}
	if !pusher.called {
		t.Fatal("pusher should be called when credentials are present")
	}
	got := pusher.got
	if got.CADir != defaultGatewayTLSCADir {
		t.Errorf("CADir = %q, want default %q", got.CADir, defaultGatewayTLSCADir)
	}
	if got.DNSProvider != defaultGatewayTLSDNSProvider {
		t.Errorf("DNSProvider = %q, want default %q", got.DNSProvider, defaultGatewayTLSDNSProvider)
	}
	if len(got.StaticDomains) != 2 || got.StaticDomains[0] != "admin.example.com" || got.StaticDomains[1] != "www.example.com" {
		t.Errorf("StaticDomains = %v, want [admin.example.com www.example.com]", got.StaticDomains)
	}
	if got.AliyunAKID != "ak" || got.AliyunAKSecret != "sk" || got.Email != "ops@example.com" {
		t.Errorf("credentials not carried through: %+v", got)
	}
}
