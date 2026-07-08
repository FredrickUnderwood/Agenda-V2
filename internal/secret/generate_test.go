package secret

import "testing"

func TestGenerateTokenShapeAndUniqueness(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(a) != 64 {
		t.Fatalf("want a 64-char hex token, got %q (len %d)", a, len(a))
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a == b {
		t.Fatal("two calls returned the same token")
	}
}
