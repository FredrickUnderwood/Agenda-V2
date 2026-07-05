package util

import "strings"

// Slug normalizes s into a docker-compose-friendly project name:
// lowercase, non-alphanumeric runs collapsed to a single '-', trimmed of
// leading/trailing '-'. Returns "app" if the result is empty.
func Slug(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			prevDash = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}
