package nodeproxy

import "testing"

func TestProxyKey(t *testing.T) {
	cases := []struct {
		app, env, instance, want string
	}{
		{"ai-sbti", "prod", "default", "ai-sbti-prod-default"},
		{"agenda-example", "prod", "default", "agenda-example-prod-default"},
		{"My App", "Prod", "Blue_1", "my-app-prod-blue-1"},
	}
	for _, c := range cases {
		if got := ProxyKey(c.app, c.env, c.instance); got != c.want {
			t.Errorf("ProxyKey(%q,%q,%q) = %q, want %q", c.app, c.env, c.instance, got, c.want)
		}
	}
	// The collision this key exists to prevent: same instance name, different apps.
	if ProxyKey("ai-sbti", "prod", "default") == ProxyKey("agenda-example", "prod", "default") {
		t.Fatal("keys for different apps must not collide")
	}
}
