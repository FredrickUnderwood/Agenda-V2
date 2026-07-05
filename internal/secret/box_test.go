package secret

import "testing"

func TestBoxRoundTrip(t *testing.T) {
	b := NewBox("super-secret-master-key")
	if !b.Enabled() {
		t.Fatal("box should be enabled with a master key")
	}
	const plaintext = "ghp_exampletoken1234567890"
	enc, err := b.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !IsEncrypted(enc) {
		t.Fatalf("encrypted value missing prefix: %q", enc)
	}
	if enc == plaintext {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := b.Decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("round trip = %q, want %q", got, plaintext)
	}
}

func TestBoxDistinctNonces(t *testing.T) {
	b := NewBox("k")
	a, _ := b.Encrypt("same")
	c, _ := b.Encrypt("same")
	if a == c {
		t.Fatal("encrypting the same plaintext twice produced identical blobs (nonce reuse)")
	}
}

func TestBoxWrongKeyFails(t *testing.T) {
	enc, _ := NewBox("right").Encrypt("data")
	if _, err := NewBox("wrong").Decrypt(enc); err == nil {
		t.Fatal("decrypt with wrong key should fail")
	}
}

func TestDisabledBoxPassthrough(t *testing.T) {
	b := NewBox("")
	if b.Enabled() {
		t.Fatal("box should be disabled without a master key")
	}
	enc, err := b.Encrypt("plain")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc != "plain" {
		t.Fatalf("disabled box should pass through, got %q", enc)
	}
	// A plaintext value round-trips through Decrypt untouched.
	got, err := b.Decrypt("plain")
	if err != nil || got != "plain" {
		t.Fatalf("decrypt passthrough = %q, %v", got, err)
	}
}

func TestDisabledBoxRejectsEncrypted(t *testing.T) {
	enc, _ := NewBox("key").Encrypt("data")
	if _, err := NewBox("").Decrypt(enc); err == nil {
		t.Fatal("disabled box must not silently return ciphertext for an encrypted value")
	}
}
