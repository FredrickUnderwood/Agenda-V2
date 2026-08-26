package filestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePath_RejectsNonAbsoluteAndDotDot(t *testing.T) {
	for _, in := range []string{"", "relative/path", "/tmp/../etc/passwd", "/tmp/./x", "/tmp//x", "/tmp/x/"} {
		if _, err := ValidatePath(in, nil); err == nil {
			t.Errorf("ValidatePath(%q) = nil error, want rejection", in)
		}
	}
}

func TestValidatePath_ConfinesToRoots(t *testing.T) {
	root := t.TempDir()
	if _, err := ValidatePath(filepath.Join(root, "a", "b.txt"), []string{root}); err != nil {
		t.Fatalf("path under root rejected: %v", err)
	}
	if _, err := ValidatePath("/etc/passwd", []string{root}); err == nil {
		t.Fatal("path outside root accepted")
	}
	// A sibling directory whose name merely starts with the root's is outside
	// it; a plain string prefix test would wrongly accept it.
	if _, err := ValidatePath(root+"-evil/x", []string{root}); err == nil {
		t.Fatal("sibling directory sharing the root's prefix accepted")
	}
}

// A symlink inside an allowed root pointing out of it must not smuggle a write
// past the confinement — the check the lexical prefix test alone cannot make.
func TestValidatePath_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ValidatePath(filepath.Join(link, "secret"), []string{root}); err == nil {
		t.Fatal("write through a symlink out of the root was accepted")
	}
}

func TestWriteAt_CreatesParentsHashesAndSetsMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "deeper", "key.p8")

	stat, err := WriteAt(target, 0o600, false, strings.NewReader("hello"), 0)
	if err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if stat.Size != 5 {
		t.Errorf("size = %d, want 5", stat.Size)
	}
	// sha256("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if stat.SHA256 != want {
		t.Errorf("sha256 = %s, want %s", stat.SHA256, want)
	}
	if stat.Mode != "0600" {
		t.Errorf("mode = %s, want 0600", stat.Mode)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat written file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("on-disk mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestWriteAt_RefusesOverwriteUnlessAsked(t *testing.T) {
	target := filepath.Join(t.TempDir(), "f")
	if _, err := WriteAt(target, 0o600, false, strings.NewReader("first"), 0); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := WriteAt(target, 0o600, false, strings.NewReader("second"), 0); err != ErrExists {
		t.Fatalf("second write err = %v, want ErrExists", err)
	}
	if _, err := WriteAt(target, 0o600, true, strings.NewReader("second"), 0); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "second" {
		t.Errorf("content = %q, want %q", got, "second")
	}
}

// A rejected oversize upload must leave nothing behind: the temporary file is
// in the destination directory, so a leaked one would sit next to real files.
func TestWriteAt_TooLargeLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "big")
	if _, err := WriteAt(target, 0o600, false, strings.NewReader("0123456789"), 4); err != ErrTooLarge {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory not clean after rejected upload: %v", entries)
	}
}

func TestStatAt_MissingIsNotAnError(t *testing.T) {
	stat, err := StatAt(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("StatAt: %v", err)
	}
	if stat.Exists {
		t.Error("Exists = true for an absent file")
	}
}

func TestStatAt_ReportsDirectoryWithoutHashing(t *testing.T) {
	stat, err := StatAt(t.TempDir())
	if err != nil {
		t.Fatalf("StatAt: %v", err)
	}
	if !stat.Exists || !stat.IsDir {
		t.Fatalf("stat = %+v, want an existing directory", stat)
	}
	if stat.SHA256 != "" {
		t.Errorf("sha256 = %q, want empty for a directory", stat.SHA256)
	}
}

func TestParseMode(t *testing.T) {
	m, err := ParseMode("")
	if err != nil || m != 0o600 {
		t.Fatalf("ParseMode(\"\") = %v, %v; want 0600", m, err)
	}
	if _, err := ParseMode("nonsense"); err == nil {
		t.Error("ParseMode accepted a non-octal string")
	}
	if got := FormatMode(0o640); got != "0640" {
		t.Errorf("FormatMode(0640) = %s", got)
	}
}
