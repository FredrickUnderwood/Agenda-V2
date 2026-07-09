package node

import (
	"bufio"
	"os"
	"sort"
	"strings"
)

// maxTailReadBytes caps how much of a log file we read from the end. Good
// enough for a self-hosted "vibe coding" scale without a real streaming tail
// implementation — avoids loading a whole rotated-but-not-yet-rotated file
// into memory.
const maxTailReadBytes = 2 << 20 // 2MB

// findLogFiles returns every file under dir named "<app>__<instance>.log",
// "<app>__<instance>__<service>.log", or with a further "__<replica>" segment —
// the naming scheme sdk/go/log's file sink uses (see AGENDA_INSTANCE_NAME /
// AGENDA_SERVICE_NAME / AGENDA_REPLICA_ID). Replicas of one scaled service each
// write their own file, so a prefix match (not an exact name) is required.
func findLogFiles(dir, app, instance string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	prefix := app + "__" + instance
	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		base, ok := strings.CutSuffix(name, ".log")
		if !ok {
			continue
		}
		if base == prefix || strings.HasPrefix(base, prefix+"__") {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

// serviceNameFromFile extracts the AGENDA_SERVICE_NAME segment (if any) from
// a "<app>__<instance>[__<service>].log" filename.
func serviceNameFromFile(file, app, instance string) string {
	base := strings.TrimSuffix(file, ".log")
	prefix := app + "__" + instance + "__"
	if svc, ok := strings.CutPrefix(base, prefix); ok {
		return svc
	}
	return ""
}

// tailLines returns up to n of the last lines in path, reading at most
// maxTailReadBytes from the end of the file.
func tailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := int64(0)
	if size := info.Size(); size > maxTailReadBytes {
		start = size - maxTailReadBytes
	}
	if _, err := f.Seek(start, 0); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if start > 0 {
		scanner.Scan() // drop a possibly-partial first line from the seek point
	}
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
