// Package filestore holds the local-filesystem half of agenda's file transfer:
// validating a target path, writing a file atomically, and describing what is
// on disk. It is shared by agenda-node (which serves /v1/files) and by
// internal/runner's localRunner, so "how a file is written to a machine" has
// exactly one implementation regardless of which side is doing the writing.
package filestore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

// Errors the file endpoints distinguish, so the control plane gets a status
// code that says what actually went wrong rather than a generic 500.
var (
	ErrPathInvalid  = errors.New("path must be absolute and free of '..' segments")
	ErrOutsideRoots = errors.New("path is outside every configured file_roots entry")
	ErrExists       = errors.New("file already exists (pass overwrite=true to replace it)")
	ErrTooLarge     = errors.New("upload exceeds max_upload_bytes")
	ErrIsDir        = errors.New("path is a directory")
)

// defaultDirMode is applied to directories created on the way to an upload
// target. It is deliberately more permissive than the file itself: the
// containers that bind-mount these directories may run as a non-root user, and
// a directory they cannot traverse hides a file they are allowed to read.
const defaultDirMode os.FileMode = 0o755

// ParseMode turns an octal permission string ("0640") into a FileMode.
// Empty means contract.DefaultFileMode.
func ParseMode(s string) (os.FileMode, error) {
	if s == "" {
		s = contract.DefaultFileMode
	}
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid mode %q: expected an octal string like 0640", s)
	}
	if n > 0o7777 {
		return 0, fmt.Errorf("invalid mode %q: out of range", s)
	}
	return os.FileMode(n), nil
}

func FormatMode(m os.FileMode) string {
	return "0" + strconv.FormatUint(uint64(m.Perm()), 8)
}

// ValidatePath rejects anything that is not a clean absolute path and, when
// roots is non-empty, anything that resolves outside them.
//
// The containment check runs against the deepest *existing* ancestor with its
// symlinks resolved, not against the literal string: a lexical prefix test alone
// is satisfied by <root>/link where link points at /etc, which is exactly the
// escape the check exists to stop.
func ValidatePath(path string, roots []string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", ErrPathInvalid
	}
	clean := filepath.Clean(path)
	if clean != path {
		// A path that changes under Clean carries "..", ".", doubled
		// separators, or a trailing slash. Normalizing it silently would let
		// the recorded path and the file on disk disagree about where the file
		// is — and the recorded path is what every later verification reads.
		return "", ErrPathInvalid
	}
	if len(roots) == 0 {
		return clean, nil
	}
	real := deepestRealPath(clean)
	for _, root := range roots {
		if root == "" {
			continue
		}
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			realRoot = filepath.Clean(root)
		}
		if real == realRoot || strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
			return clean, nil
		}
	}
	return "", ErrOutsideRoots
}

// deepestRealPath resolves symlinks over the longest existing prefix of path
// and re-appends the part that does not exist yet, so an upload to a not-yet-
// created file still gets its parent directories resolved.
func deepestRealPath(path string) string {
	cur := filepath.Clean(path)
	rest := ""
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the filesystem root without finding anything that exists.
			return filepath.Join(cur, rest)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// WriteAt streams src to path and reports what ended up on disk.
//
// The bytes land in a temporary file in the destination directory and are
// renamed into place only once they are all written and fsynced. A consumer of
// this file — an app container reading a credential at startup — must never be
// able to observe a half-written one, and rename within a directory is the only
// way to guarantee that.
func WriteAt(path string, mode os.FileMode, overwrite bool, src io.Reader, maxBytes int64) (contract.FileStat, error) {
	switch existing, err := os.Lstat(path); {
	case err == nil && existing.IsDir():
		return contract.FileStat{}, ErrIsDir
	case err == nil && !overwrite:
		return contract.FileStat{}, ErrExists
	case err != nil && !os.IsNotExist(err):
		return contract.FileStat{}, err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, defaultDirMode); err != nil {
		return contract.FileStat{}, err
	}

	tmp, err := os.CreateTemp(dir, ".agenda-upload-*")
	if err != nil {
		return contract.FileStat{}, err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	reader := src
	if maxBytes > 0 {
		// +1 so the copy runs one byte past the limit and the overshoot is
		// detectable; a plain LimitReader would silently truncate instead.
		reader = io.LimitReader(src, maxBytes+1)
	}
	written, err := io.Copy(io.MultiWriter(tmp, hasher), reader)
	if err != nil {
		return contract.FileStat{}, err
	}
	if maxBytes > 0 && written > maxBytes {
		return contract.FileStat{}, ErrTooLarge
	}
	if err := tmp.Sync(); err != nil {
		return contract.FileStat{}, err
	}
	if err := tmp.Chmod(mode); err != nil {
		return contract.FileStat{}, err
	}
	if err := tmp.Close(); err != nil {
		return contract.FileStat{}, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return contract.FileStat{}, err
	}
	committed = true

	return contract.FileStat{
		Exists:      true,
		Size:        written,
		Mode:        FormatMode(mode),
		ModTimeUnix: modTimeOf(path),
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func modTimeOf(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().Unix()
}

// StatAt describes a file on this machine, hashing its current contents so
// the control plane can tell "still the file we uploaded" from "someone
// replaced it". A missing file is not an error — "it is gone" is a legitimate
// answer to the question and the caller records it as such.
func StatAt(path string) (contract.FileStat, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return contract.FileStat{Exists: false}, nil
	}
	if err != nil {
		return contract.FileStat{}, err
	}
	out := contract.FileStat{
		Exists:      true,
		IsDir:       info.IsDir(),
		Size:        info.Size(),
		Mode:        FormatMode(info.Mode()),
		ModTimeUnix: info.ModTime().Unix(),
	}
	if info.IsDir() {
		return out, nil
	}
	sum, err := hashFile(path)
	if err != nil {
		return contract.FileStat{}, err
	}
	out.SHA256 = sum
	return out, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
