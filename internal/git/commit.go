package git

import (
	"errors"
	"strings"
)

// shortCommitSHALen is the shortest abbreviation accepted for a pinned commit.
// Seven is git's own historical default for `--abbrev`, and short enough to
// stay collision-free in any repository small enough to deploy from here;
// anything shorter is ambiguous often enough that `git reset` would start
// failing on a repo that merely grew.
const shortCommitSHALen = 7

// fullCommitSHALen is the length of a full SHA-1 object name.
const fullCommitSHALen = 40

// NormalizeCommitSHA validates an operator-supplied commit pin and returns it
// lowercased. An empty string is valid and means "whatever the branch points
// at now" — that is the normal deploy path.
//
// Both an abbreviation (8ce16504d4) and a full object name
// (8ce16504d48b6d3ed1e61ed6819320c2c910e413) are accepted: git resolves either
// one, and requiring the full 40 characters would make the field unusable for
// anyone copying a SHA out of a UI that abbreviates. What actually gets stored
// on the release is always the full 40-character SHA regardless, because
// git_pull resolves the checked-out HEAD after the fact (see GitPullStep) —
// so an abbreviation is only ever an input convenience, never something a
// later rollback has to resolve again.
func NormalizeCommitSHA(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", nil
	}
	if len(s) < shortCommitSHALen || len(s) > fullCommitSHALen {
		return "", errors.New("commit SHA must be between 7 and 40 hex characters (an abbreviation like 8ce16504d4, or the full object name)")
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", errors.New("commit SHA must be hexadecimal; got \"" + raw + "\"")
		}
	}
	return s, nil
}
