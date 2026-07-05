package git

import (
	"errors"
	"net/url"
	"strings"
)

// ValidateRepoURL ensures a repo URL is an http(s) URL suitable for
// token-based authentication. SSH-style URLs are rejected here because
// Docker-deployed backends typically don't have SSH keys/known_hosts
// configured; we want users to fail fast at config time.
func ValidateRepoURL(rawURL string) error {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return errors.New("repo url is required")
	}

	if looksLikeSCPStyleSSH(s) {
		return errors.New("SSH-style repo URL \"" + s + "\" is not supported; please use https://... with a Personal Access Token")
	}

	u, err := url.Parse(s)
	if err != nil {
		return errors.New("parse repo url: " + err.Error())
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
	case "ssh", "git":
		return errors.New(scheme + ":// repo URL is not supported; please use https://... with a Personal Access Token")
	case "":
		return errors.New("repo url \"" + s + "\" has no scheme; please use https://...")
	default:
		return errors.New("unsupported repo URL scheme \"" + scheme + "\"; please use https://...")
	}

	if u.Host == "" {
		return errors.New("repo url \"" + s + "\" is missing host")
	}
	return nil
}

// looksLikeSCPStyleSSH matches "user@host:path" style URLs (the form git uses
// for SSH without an ssh:// scheme).
func looksLikeSCPStyleSSH(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	at := strings.Index(s, "@")
	if at <= 0 {
		return false
	}
	colon := strings.Index(s[at:], ":")
	return colon > 0
}
