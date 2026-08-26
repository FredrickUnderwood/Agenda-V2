// Package sqlguard decides whether a single SQL statement is safe to run
// through the console's read-only query path. It is imported by both the
// control plane (which rejects early, with a good error message) and
// agenda-node (which re-checks what it was actually handed), so the two ends
// can never drift.
//
// This is deliberately a syntactic gate, not a security boundary. A determined
// caller with write privileges could eventually find a phrasing this does not
// model. The actual boundary is the database account the instance is
// registered with — grant it SELECT and nothing else — backed by the
// session-level transaction_read_only the node sets before every statement.
// See doc/rds.md.
package sqlguard

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ErrRejected wraps every rejection so callers can map the whole package to a
// single HTTP status (400) with errors.Is.
var ErrRejected = errors.New("statement rejected")

// readOnlyVerbs are the statement kinds the console will run. Everything else
// — including anything that writes, changes schema, or manages the server — is
// refused. WITH is included for common table expressions: MySQL has no
// WITH ... UPDATE form, so a statement starting with WITH is always a read.
var readOnlyVerbs = map[string]bool{
	"SELECT":   true,
	"WITH":     true,
	"SHOW":     true,
	"DESC":     true,
	"DESCRIBE": true,
	"EXPLAIN":  true,
}

// outfileRe matches the SELECT ... INTO OUTFILE / INTO DUMPFILE forms, which
// write a file on the database server. Running it needs the FILE privilege, so
// a correctly-provisioned account already refuses — this rejects it a layer
// earlier, and with a comprehensible message.
var outfileRe = regexp.MustCompile(`(?i)\binto\s+(outfile|dumpfile)\b`)

// EnsureReadOnly validates stmt and returns the normalized form to execute:
// comments stripped, surrounding whitespace and any trailing semicolon
// removed. Callers should run the returned string rather than the original, so
// what was validated is exactly what reaches the database.
func EnsureReadOnly(stmt string) (string, error) {
	code, masked, err := scan(stmt)
	if err != nil {
		return "", err
	}
	code, masked = trimStatement(code, masked)
	if len(code) == 0 {
		return "", fmt.Errorf("%w: statement is empty", ErrRejected)
	}

	// Any semicolon left after the trailing ones were trimmed means a second
	// statement is riding along. The driver is configured without
	// multiStatements so it would not execute anyway, but failing here gives a
	// real error instead of a driver syntax error.
	if containsRune(masked, ';') {
		return "", fmt.Errorf("%w: only one statement may be run at a time", ErrRejected)
	}

	verb := leadingVerb(masked)
	if verb == "" {
		return "", fmt.Errorf("%w: could not determine the statement type", ErrRejected)
	}
	if !readOnlyVerbs[verb] {
		return "", fmt.Errorf("%w: %s is not a read-only statement; this console runs SELECT, WITH, SHOW, DESCRIBE and EXPLAIN only", ErrRejected, verb)
	}

	if outfileRe.MatchString(string(masked)) {
		return "", fmt.Errorf("%w: INTO OUTFILE / INTO DUMPFILE writes a file on the database server", ErrRejected)
	}

	return string(code), nil
}

// scan walks stmt once and returns two equal-length rune slices: code, with
// comments removed and string literals intact (this is what gets executed),
// and masked, identical except that the contents of quoted literals are
// blanked to spaces. Every subsequent check runs against masked, so a
// semicolon or the word "outfile" sitting inside a string literal cannot
// trigger a false rejection while still occupying the same index in code.
func scan(stmt string) (code, masked []rune, err error) {
	src := []rune(stmt)
	n := len(src)
	code = make([]rune, 0, n)
	masked = make([]rune, 0, n)

	emit := func(c, m rune) {
		code = append(code, c)
		masked = append(masked, m)
	}

	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			quote := c
			emit(c, c)
			i++
			closed := false
			for i < n {
				// A backslash escape (MySQL's default mode) hides the next
				// rune, including a closing quote. Backtick identifiers do not
				// honor backslash escapes.
				if src[i] == '\\' && quote != '`' && i+1 < n {
					emit(src[i], ' ')
					emit(src[i+1], ' ')
					i += 2
					continue
				}
				if src[i] == quote {
					// A doubled quote is an escaped quote, not the end.
					if i+1 < n && src[i+1] == quote {
						emit(src[i], ' ')
						emit(src[i+1], ' ')
						i += 2
						continue
					}
					emit(src[i], src[i])
					i++
					closed = true
					break
				}
				emit(src[i], ' ')
				i++
			}
			if !closed {
				return nil, nil, fmt.Errorf("%w: unterminated quoted literal", ErrRejected)
			}

		case c == '-' && i+1 < n && src[i+1] == '-' && (i+2 >= n || isSpaceRune(src[i+2])):
			// MySQL only treats "--" as a comment when whitespace follows, so
			// "a--b" stays an expression.
			for i < n && src[i] != '\n' {
				i++
			}

		case c == '#':
			for i < n && src[i] != '\n' {
				i++
			}

		case c == '/' && i+1 < n && src[i+1] == '*':
			// /*! ... */ is an executable comment: MySQL runs what is inside
			// it. Treating it as a comment would let a write hide behind a
			// SELECT, so refuse the statement outright.
			if i+2 < n && src[i+2] == '!' {
				return nil, nil, fmt.Errorf("%w: executable comments (/*! ... */) are not allowed", ErrRejected)
			}
			j := i + 2
			for j+1 < n && !(src[j] == '*' && src[j+1] == '/') {
				j++
			}
			if j+1 >= n {
				return nil, nil, fmt.Errorf("%w: unterminated block comment", ErrRejected)
			}
			// Collapse to one space so the tokens either side stay separate.
			emit(' ', ' ')
			i = j + 2

		default:
			emit(c, c)
			i++
		}
	}
	return code, masked, nil
}

// trimStatement drops leading whitespace and trailing whitespace/semicolons
// from both slices at the same indices, keeping them aligned.
func trimStatement(code, masked []rune) ([]rune, []rune) {
	end := len(masked)
	for end > 0 && (isSpaceRune(masked[end-1]) || masked[end-1] == ';') {
		end--
	}
	start := 0
	for start < end && isSpaceRune(masked[start]) {
		start++
	}
	return code[start:end], masked[start:end]
}

// leadingVerb returns the uppercased first keyword, skipping any opening
// parentheses so a parenthesized union — "(SELECT 1) UNION (SELECT 2)" — is
// still recognized as a SELECT.
func leadingVerb(masked []rune) string {
	i := 0
	for i < len(masked) && (isSpaceRune(masked[i]) || masked[i] == '(') {
		i++
	}
	j := i
	for j < len(masked) && (unicode.IsLetter(masked[j]) || masked[j] == '_') {
		j++
	}
	return strings.ToUpper(string(masked[i:j]))
}

func containsRune(rs []rune, target rune) bool {
	for _, r := range rs {
		if r == target {
			return true
		}
	}
	return false
}

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v'
}
