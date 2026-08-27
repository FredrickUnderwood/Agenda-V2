// Package redisguard decides whether a single Redis command is safe to run
// through the console's read-only path. Like sqlguard it is imported by both
// the control plane (which rejects early, with a good error message) and
// agenda-node (which re-checks what it was actually handed), so the two ends
// can never drift.
//
// Redis makes this simpler than SQL: a command is one verb, so the gate is an
// allowlist of verbs rather than an attempt to model a grammar. Anything not
// listed is refused, which means a command added by a future Redis version is
// refused until someone looks at it — the safe direction for a default.
//
// As with sqlguard, this is not the security boundary. The boundary is the
// Redis account the instance is registered with: give it an ACL that allows
// only reads (`ACL SETUSER agenda_ro on >... ~* +@read +@connection`). See
// doc/rds.md.
package redisguard

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ErrRejected wraps every rejection so callers can map the whole package to a
// single HTTP status (400) with errors.Is.
var ErrRejected = errors.New("command rejected")

// readOnlyCommands is the allowlist. A nil value means the command takes any
// arguments; a non-nil set means the first argument must be one of those
// subcommands, which is how the container commands (OBJECT, XINFO, CONFIG …)
// are kept to their reading halves.
//
// Deliberately absent, and why:
//
//   - Anything that writes, obviously — and the ones that do not look like it:
//     GETDEL, GETEX, PFCOUNT (it rewrites the cached cardinality), SORT (it
//     takes STORE; SORT_RO is allowed instead), GEORADIUS (also STORE).
//   - Blocking or streaming commands — BLPOP, XREAD, SUBSCRIBE, MONITOR, WAIT.
//     They would hold the connection past the statement timeout and return
//     nothing useful to a request/response console.
//   - Server administration — CONFIG SET, CLIENT KILL, FLUSHDB, DEBUG, SHUTDOWN.
//   - SELECT and SWAPDB: the DB a command runs against is chosen in the console
//     and recorded in the audit trail, so a command must not be able to move
//     itself somewhere else.
//   - EVAL and friends: a script's contents are opaque to any allowlist.
var readOnlyCommands = map[string]map[string]bool{
	// Connection and server introspection.
	"PING":     nil,
	"ECHO":     nil,
	"TIME":     nil,
	"DBSIZE":   nil,
	"INFO":     nil,
	"LASTSAVE": nil,
	"CONFIG":   {"GET": true},
	"MEMORY":   {"USAGE": true, "DOCTOR": true, "STATS": true},
	"CLIENT":   {"INFO": true, "GETNAME": true, "ID": true},

	// Keyspace.
	"EXISTS":      nil,
	"TYPE":        nil,
	"TTL":         nil,
	"PTTL":        nil,
	"EXPIRETIME":  nil,
	"PEXPIRETIME": nil,
	"KEYS":        nil,
	"SCAN":        nil,
	"RANDOMKEY":   nil,
	"OBJECT":      {"ENCODING": true, "FREQ": true, "IDLETIME": true, "REFCOUNT": true},
	"SORT_RO":     nil,

	// Strings and bitmaps.
	"GET":      nil,
	"MGET":     nil,
	"GETRANGE": nil,
	"SUBSTR":   nil,
	"STRLEN":   nil,
	"LCS":      nil,
	"GETBIT":   nil,
	"BITCOUNT": nil,
	"BITPOS":   nil,

	// Hashes.
	"HGET":       nil,
	"HMGET":      nil,
	"HGETALL":    nil,
	"HKEYS":      nil,
	"HVALS":      nil,
	"HLEN":       nil,
	"HEXISTS":    nil,
	"HSTRLEN":    nil,
	"HSCAN":      nil,
	"HRANDFIELD": nil,

	// Lists.
	"LRANGE": nil,
	"LLEN":   nil,
	"LINDEX": nil,
	"LPOS":   nil,

	// Sets.
	"SMEMBERS":    nil,
	"SISMEMBER":   nil,
	"SMISMEMBER":  nil,
	"SCARD":       nil,
	"SRANDMEMBER": nil,
	"SSCAN":       nil,
	"SINTER":      nil,
	"SUNION":      nil,
	"SDIFF":       nil,
	"SINTERCARD":  nil,

	// Sorted sets. The STORE-taking forms are separate commands
	// (ZRANGESTORE, ZUNIONSTORE …) and are simply not listed.
	"ZRANGE":           nil,
	"ZREVRANGE":        nil,
	"ZRANGEBYSCORE":    nil,
	"ZREVRANGEBYSCORE": nil,
	"ZRANGEBYLEX":      nil,
	"ZREVRANGEBYLEX":   nil,
	"ZSCORE":           nil,
	"ZMSCORE":          nil,
	"ZCARD":            nil,
	"ZCOUNT":           nil,
	"ZLEXCOUNT":        nil,
	"ZRANK":            nil,
	"ZREVRANK":         nil,
	"ZSCAN":            nil,
	"ZRANDMEMBER":      nil,
	"ZDIFF":            nil,
	"ZINTER":           nil,
	"ZUNION":           nil,
	"ZINTERCARD":       nil,

	// Geo. GEOSEARCH replaced GEORADIUS precisely because the older form
	// carried a STORE option.
	"GEOPOS":    nil,
	"GEODIST":   nil,
	"GEOHASH":   nil,
	"GEOSEARCH": nil,

	// Streams.
	"XRANGE":    nil,
	"XREVRANGE": nil,
	"XLEN":      nil,
	"XPENDING":  nil,
	"XINFO":     {"STREAM": true, "GROUPS": true, "CONSUMERS": true},
}

// EnsureReadOnly parses one command line and returns its arguments, ready to be
// sent as a RESP array. Callers send the returned slice rather than the
// original text, so what was validated is exactly what reaches the server.
func EnsureReadOnly(command string) ([]string, error) {
	args, err := split(command)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("%w: command is empty", ErrRejected)
	}

	verb := strings.ToUpper(args[0])
	subcommands, listed := readOnlyCommands[verb]
	if !listed {
		return nil, fmt.Errorf("%w: %s is not one of the read-only commands this console runs", ErrRejected, verb)
	}
	if subcommands != nil {
		if len(args) < 2 {
			return nil, fmt.Errorf("%w: %s needs a subcommand; %s", ErrRejected, verb, allowedSubcommands(verb, subcommands))
		}
		if !subcommands[strings.ToUpper(args[1])] {
			return nil, fmt.Errorf("%w: %s %s is not read-only; %s", ErrRejected, verb, strings.ToUpper(args[1]), allowedSubcommands(verb, subcommands))
		}
	}
	return args, nil
}

func allowedSubcommands(verb string, subcommands map[string]bool) string {
	names := make([]string, 0, len(subcommands))
	for name := range subcommands {
		names = append(names, name)
	}
	// Small fixed sets; sort so the message is stable rather than map-ordered.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return "only " + verb + " " + strings.Join(names, " / ") + " may be run"
}

// split breaks a command line into arguments the way redis-cli does: on
// whitespace, with single or double quotes grouping an argument that contains
// spaces and a backslash escaping the next character inside double quotes.
//
// It stops short of redis-cli's \xNN escapes. A binary key that cannot be typed
// is a real limitation, but inventing a half-implementation of that syntax
// would be worse: an argument would then mean something different here than it
// does in the client the operator is used to.
func split(command string) ([]string, error) {
	var (
		args    []string
		current strings.Builder
		started bool
	)
	runes := []rune(command)

	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\'' || c == '"':
			quote := c
			started = true
			i++
			closed := false
			for i < len(runes) {
				if quote == '"' && runes[i] == '\\' && i+1 < len(runes) {
					current.WriteRune(runes[i+1])
					i += 2
					continue
				}
				if runes[i] == quote {
					closed = true
					break
				}
				current.WriteRune(runes[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unterminated quoted argument", ErrRejected)
			}

		case unicode.IsSpace(c):
			if started {
				args = append(args, current.String())
				current.Reset()
				started = false
			}

		default:
			started = true
			current.WriteRune(c)
		}
	}
	if started {
		args = append(args, current.String())
	}
	return args, nil
}
