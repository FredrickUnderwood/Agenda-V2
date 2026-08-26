package service

import (
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
)

// The console shows the full upload history, so exactly one row per
// (machine, path) must be flagged as describing the file on disk — otherwise a
// superseded row's checksum reads as the current state.
func TestMarkCurrent_FlagsOnlyTheNewestRowPerMachineAndPath(t *testing.T) {
	// Newest first, matching the order both list queries return.
	rows := []*domain.MachineFile{
		{ID: 5, MachineID: 1, Path: "/run/app/prod/.files/key.p8"},
		{ID: 4, MachineID: 2, Path: "/run/app/prod/.files/key.p8"},
		{ID: 3, MachineID: 1, Path: "/run/app/prod/.files/other.pem"},
		{ID: 2, MachineID: 1, Path: "/run/app/prod/.files/key.p8"},
		{ID: 1, MachineID: 2, Path: "/run/app/prod/.files/key.p8"},
	}
	markCurrent(rows)

	want := map[int64]bool{5: true, 4: true, 3: true, 2: false, 1: false}
	for _, r := range rows {
		if r.Current != want[r.ID] {
			t.Errorf("row %d: Current = %v, want %v", r.ID, r.Current, want[r.ID])
		}
	}
}

// The same file name on two different machines is two independent records, not
// one superseding the other.
func TestMarkCurrent_SameNameOnDifferentMachinesAreBothCurrent(t *testing.T) {
	rows := []*domain.MachineFile{
		{ID: 2, MachineID: 7, Path: "/files/key.p8"},
		{ID: 1, MachineID: 8, Path: "/files/key.p8"},
	}
	markCurrent(rows)
	for _, r := range rows {
		if !r.Current {
			t.Errorf("row %d on machine %d should be current", r.ID, r.MachineID)
		}
	}
}

func TestFileNamePattern(t *testing.T) {
	valid := []string{"key.p8", "apple-key_1.pem", "a", "0cert.crt"}
	for _, name := range valid {
		if !fileNamePattern.MatchString(name) {
			t.Errorf("%q should be a valid file name", name)
		}
	}
	// A name becomes a path component on every machine and a filename inside
	// every container, so anything that could traverse or confuse either is out.
	invalid := []string{"", ".", "..", "../escape", "dir/key.p8", "-rf", "key p8", "key;rm", ".hidden"}
	for _, name := range invalid {
		if fileNamePattern.MatchString(name) {
			t.Errorf("%q should be rejected as a file name", name)
		}
	}
}
