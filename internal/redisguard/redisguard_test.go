package redisguard

import (
	"errors"
	"strings"
	"testing"
)

func TestEnsureReadOnlyAccepts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"get", "GET user:1", []string{"GET", "user:1"}},
		{"lowercase verb", "get user:1", []string{"get", "user:1"}},
		{"surrounding space", "  TTL  user:1  ", []string{"TTL", "user:1"}},
		{"scan with options", "SCAN 0 MATCH user:* COUNT 100", []string{"SCAN", "0", "MATCH", "user:*", "COUNT", "100"}},
		{"double quoted argument", `GET "a key with spaces"`, []string{"GET", "a key with spaces"}},
		{"single quoted argument", `GET 'a key with spaces'`, []string{"GET", "a key with spaces"}},
		{"escaped quote inside double quotes", `GET "say \"hi\""`, []string{"GET", `say "hi"`}},
		{"empty quoted argument is kept", `HGET h ""`, []string{"HGET", "h", ""}},
		{"container subcommand", "OBJECT ENCODING user:1", []string{"OBJECT", "ENCODING", "user:1"}},
		{"container subcommand lowercase", "config get maxmemory", []string{"config", "get", "maxmemory"}},
		{"sort_ro", "SORT_RO mylist LIMIT 0 10", []string{"SORT_RO", "mylist", "LIMIT", "0", "10"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EnsureReadOnly(tc.in)
			if err != nil {
				t.Fatalf("EnsureReadOnly(%q) = error %v, want accepted", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("EnsureReadOnly(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("EnsureReadOnly(%q) = %q, want %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

func TestEnsureReadOnlyRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", "   "},
		{"set", "SET user:1 hello"},
		{"del", "DEL user:1"},
		{"flushdb", "FLUSHDB"},
		{"flushall", "FLUSHALL"},
		{"rename", "RENAME a b"},
		{"expire", "EXPIRE user:1 60"},
		// Reads with a write hiding in them.
		{"getdel", "GETDEL user:1"},
		{"getex", "GETEX user:1 EX 60"},
		{"pfcount rewrites its cache", "PFCOUNT hll"},
		{"sort takes STORE", "SORT mylist STORE dest"},
		{"georadius takes STORE", "GEORADIUS k 1 1 1 km STORE dest"},
		{"zrangestore", "ZRANGESTORE dest src 0 -1"},
		{"bitop", "BITOP AND dest a b"},
		// Scripting is opaque to an allowlist.
		{"eval", "EVAL \"return 1\" 0"},
		{"evalsha", "EVALSHA abc 0"},
		{"function", "FUNCTION LIST"},
		// Blocking or streaming.
		{"blpop", "BLPOP q 0"},
		{"subscribe", "SUBSCRIBE ch"},
		{"monitor", "MONITOR"},
		{"xread can block", "XREAD COUNT 1 STREAMS s 0"},
		// Administration.
		{"config set", "CONFIG SET maxmemory 100mb"},
		{"config resetstat", "CONFIG RESETSTAT"},
		{"client kill", "CLIENT KILL ID 4"},
		{"debug", "DEBUG SLEEP 10"},
		{"shutdown", "SHUTDOWN"},
		{"replicaof", "REPLICAOF host 6379"},
		// The DB index is chosen in the console and recorded; a command must
		// not be able to move itself somewhere else.
		{"select", "SELECT 3"},
		{"swapdb", "SWAPDB 0 1"},
		{"move", "MOVE user:1 2"},
		// A container command with a write-capable subcommand.
		{"object help is not listed", "OBJECT HELP"},
		{"xinfo without subcommand", "XINFO"},
		{"unterminated quote", `GET "unclosed`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EnsureReadOnly(tc.in)
			if err == nil {
				t.Fatalf("EnsureReadOnly(%q) was accepted, want rejected", tc.in)
			}
			if !errors.Is(err, ErrRejected) {
				t.Fatalf("EnsureReadOnly(%q) error %v does not wrap ErrRejected", tc.in, err)
			}
		})
	}
}

// The rejection has to say what was wrong with it, not merely that something
// was: the console shows this message verbatim.
func TestRejectionNamesTheCommand(t *testing.T) {
	_, err := EnsureReadOnly("SET k v")
	if err == nil || !strings.Contains(err.Error(), "SET") {
		t.Fatalf("error %v should name the refused command", err)
	}
	_, err = EnsureReadOnly("CONFIG SET maxmemory 1gb")
	if err == nil || !strings.Contains(err.Error(), "CONFIG GET") {
		t.Fatalf("error %v should say which subcommand is allowed instead", err)
	}
}
