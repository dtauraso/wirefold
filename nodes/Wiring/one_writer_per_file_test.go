package Wiring

// one_writer_per_file_test.go — pins fact #2 of the one-file-per-goroutine split
// (docs/planning/visual-editor/one-file-per-goroutine.md): each of the five NEW files this
// split introduced (camera.json, overlays.json, sphere.json, position.json,
// local-polars.json) has its ON-DISK NAME literal spelled out in exactly ONE place in
// production (non-test) source — the single path-building function for that file
// (cameraFilePath / overlaysFilePath / sphereFilePath / positionFilePath /
// localPolarsFilePath). Every writer, loader and persister-arming call site reaches the file
// only through that one function; nothing else is allowed to spell the filename itself. A
// second writer appearing later — the exact way sceneFileMu and entityFileMus were born in
// the first place — would need to either reuse the existing path helper (in which case grep
// for its call sites, not this test, is the tripwire) or hand-roll the filename again, which
// this test catches directly.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countLiteralOccurrences counts how many non-test .go files in this package's directory
// contain the exact literal (e.g. `"camera.json"`), and how many total occurrences there
// are — a second definition of the same filename anywhere in production source pushes this
// above 1.
func countLiteralOccurrences(t *testing.T, literal string) int {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	count := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		count += strings.Count(string(raw), literal)
	}
	return count
}

// TestEachSplitFileNameIsSpelledExactlyOnce asserts each new file's on-disk name literal
// appears in production source exactly once — proof there is exactly one place that can
// ever construct a path to it, and therefore exactly one writer.
func TestEachSplitFileNameIsSpelledExactlyOnce(t *testing.T) {
	names := []string{
		`"camera.json"`,
		`"overlays.json"`,
		`"sphere.json"`,
		`"position.json"`,
		`"local-polars.json"`,
	}
	for _, name := range names {
		got := countLiteralOccurrences(t, name)
		if got != 1 {
			t.Fatalf("filename literal %s appears %d time(s) in production source, want exactly 1 "+
				"(one path-building function) — a second spelling is how a second writer sneaks in", name, got)
		}
	}
}
