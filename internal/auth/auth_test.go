package auth

import (
	"testing"
	"time"
)

func TestIssueVerifyRoundTrip(t *testing.T) {
	m := NewManager("shared-secret", "", time.Hour)
	tok, err := m.Issue(7, "alice", RoleAdmin)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	id, err := m.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.UserID != 7 || id.Username != "alice" || id.Role != RoleAdmin {
		t.Fatalf("identity = %+v", id)
	}
	if !id.Has("anything") {
		t.Fatal("admin should hold the wildcard perm")
	}
}

func TestMemberHasNoWildcard(t *testing.T) {
	m := NewManager("s", "", time.Hour)
	tok, _ := m.Issue(1, "bob", RoleMember)
	id, _ := m.Verify(tok)
	if id.Has("setting.write") {
		t.Fatal("member should not hold setting.write")
	}
}

func TestVerifyWrongSecretFails(t *testing.T) {
	tok, _ := NewManager("right", "", time.Hour).Issue(1, "a", RoleAdmin)
	if _, err := NewManager("wrong", "", time.Hour).Verify(tok); err == nil {
		t.Fatal("verify with wrong secret should fail")
	}
}

func TestVerifyExpiredFails(t *testing.T) {
	m := NewManager("s", "", -time.Minute) // already expired... but ttl<=0 defaults to 24h
	// Force an expired token by using a tiny positive ttl and sleeping is slow;
	// instead build a manager with a real ttl and a manually expired issue is
	// not exposed, so use a 1ns ttl.
	m = &Manager{secret: []byte("s"), ttl: time.Nanosecond}
	tok, _ := m.Issue(1, "a", RoleAdmin)
	time.Sleep(2 * time.Millisecond)
	if _, err := m.Verify(tok); err == nil {
		t.Fatal("expired token should fail verification")
	}
}

func TestEnabledAndCanIssue(t *testing.T) {
	if NewManager("", "", 0).Enabled() {
		t.Fatal("no secret and no static token → disabled")
	}
	if !NewManager("", "static", 0).Enabled() {
		t.Fatal("static token → enabled")
	}
	if NewManager("", "static", 0).CanIssue() {
		t.Fatal("static token alone cannot issue JWTs")
	}
	if !NewManager("secret", "", 0).CanIssue() {
		t.Fatal("secret → can issue")
	}
}
