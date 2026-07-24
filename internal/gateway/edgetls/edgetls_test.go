package edgetls

import (
	"testing"
	"time"
)

func validOptions() Options {
	return Options{
		Enabled:            true,
		HTTPSAddr:          ":443",
		Resolvers:          []string{"223.5.5.5"},
		PropagationTimeout: 2 * time.Minute,
		StoragePath:        "/data",
		ReconcileInterval:  30 * time.Second,
	}
}

func validConfig() Config {
	return Config{
		Email:          "ops@example.com",
		CADir:          "https://acme.zerossl.com/v2/DV90",
		EABKeyID:       "kid",
		EABHMACKey:     "hmac",
		DNSProvider:    DNSProviderAlidns,
		AliyunAKID:     "ak",
		AliyunAKSecret: "sk",
	}
}

func TestOptionsValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Options)
		wantErr bool
	}{
		{"valid", func(*Options) {}, false},
		{"missing addr", func(o *Options) { o.HTTPSAddr = "" }, true},
		{"missing storage", func(o *Options) { o.StoragePath = "" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := validOptions()
			tc.mutate(&o)
			if got := o.validate() != nil; got != tc.wantErr {
				t.Fatalf("Options.validate() err? = %v, wantErr = %v", got, tc.wantErr)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid", func(*Config) {}, false},
		{"wrong dns provider", func(c *Config) { c.DNSProvider = "route53" }, true},
		{"missing ak", func(c *Config) { c.AliyunAKID = "" }, true},
		{"missing email", func(c *Config) { c.Email = "" }, true},
		{"missing ca", func(c *Config) { c.CADir = "" }, true},
		{"half eab", func(c *Config) { c.EABHMACKey = "" }, true},
		{"no eab is ok", func(c *Config) { c.EABKeyID = ""; c.EABHMACKey = "" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(&c)
			if got := c.validate() != nil; got != tc.wantErr {
				t.Fatalf("Config.validate() err? = %v, wantErr = %v", got, tc.wantErr)
			}
		})
	}
}

func TestConfigFingerprintStable(t *testing.T) {
	a := validConfig()
	b := validConfig()
	b.StaticDomains = []string{"b.example.com", "a.example.com"}
	a.StaticDomains = []string{"a.example.com", "b.example.com"}
	if a.fingerprint() != b.fingerprint() {
		t.Fatal("fingerprint should be order-independent for static domains")
	}
	b.AliyunAKSecret = "rotated"
	if a.fingerprint() == b.fingerprint() {
		t.Fatal("fingerprint should change when a credential changes")
	}
}

func TestNewDisabledReturnsNil(t *testing.T) {
	mgr, err := New(Options{Enabled: false})
	if err != nil {
		t.Fatalf("New disabled err = %v", err)
	}
	if mgr != nil {
		t.Fatalf("New disabled should return nil manager, got %v", mgr)
	}
}

func TestReconfigureThenReady(t *testing.T) {
	mgr, err := New(validOptions())
	if err != nil {
		t.Fatalf("New err = %v", err)
	}
	if mgr.Ready() {
		t.Fatal("manager should not be ready before Reconfigure")
	}
	if err := mgr.Reconfigure(validConfig()); err != nil {
		t.Fatalf("Reconfigure err = %v", err)
	}
	if !mgr.Ready() {
		t.Fatal("manager should be ready after a valid Reconfigure")
	}
	// Invalid config is rejected and does not flip readiness off.
	bad := validConfig()
	bad.AliyunAKID = ""
	if err := mgr.Reconfigure(bad); err == nil {
		t.Fatal("Reconfigure should reject invalid config")
	}
	if !mgr.Ready() {
		t.Fatal("a rejected Reconfigure must not clear prior readiness")
	}
}

func TestDesiredDomains(t *testing.T) {
	m := &Manager{
		source: func() []string {
			return []string{"*", "API.example.com", "admin.example.com"}
		},
	}
	got := m.desiredDomains([]string{"Admin.example.com", "web.example.com", " web.example.com "})
	want := []string{"admin.example.com", "api.example.com", "web.example.com"}
	if len(got) != len(want) {
		t.Fatalf("desiredDomains() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("desiredDomains()[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
