package repo

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gregpriday/codeexpert/internal/config"
)

// scanLinesOracle reproduces bufio.Scanner's line splitting, the behavior
// LineOffsets must stay consistent with.
func scanLinesOracle(data []byte) []string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out []string
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

// extractLine mirrors index.lineAt: slice line i using the offset table and
// strip a trailing carriage return.
func extractLine(data []byte, offsets []int32, i int) string {
	start := int(offsets[i])
	var end int
	if i+1 < len(offsets) {
		end = int(offsets[i+1]) - 1
	} else {
		end = len(data)
		if nl := bytes.IndexByte(data[start:], '\n'); nl >= 0 {
			end = start + nl
		}
	}
	line := data[start:end]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return string(line)
}

func TestLineOffsets(t *testing.T) {
	cases := []struct {
		name string
		data string
		want []int32
	}{
		{"empty", "", nil},
		{"single no newline", "a", []int32{0}},
		{"single trailing newline", "a\n", []int32{0}},
		{"two no trailing", "a\nb", []int32{0, 2}},
		{"two trailing", "a\nb\n", []int32{0, 2}},
		{"crlf", "a\r\nb\r\n", []int32{0, 3}},
		{"blank line in middle", "a\n\nb\n", []int32{0, 2, 3}},
		{"trailing blank line", "a\n\n", []int32{0, 2}},
		{"only newline", "\n", []int32{0}},
		{"newlines only", "\n\n\n", []int32{0, 1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.data)
			got := LineOffsets(data)
			if len(got) != len(tc.want) {
				t.Fatalf("LineOffsets(%q) = %v, want %v", tc.data, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("LineOffsets(%q) = %v, want %v", tc.data, got, tc.want)
				}
			}
			// Consistency with bufio.Scanner: one offset per scanned line, and
			// slicing by offsets reproduces each scanned line.
			oracle := scanLinesOracle(data)
			if len(got) != len(oracle) {
				t.Fatalf("offset count %d != scan line count %d for %q", len(got), len(oracle), tc.data)
			}
			for i := range oracle {
				if line := extractLine(data, got, i); line != oracle[i] {
					t.Errorf("line %d via offsets = %q, scanner = %q", i, line, oracle[i])
				}
			}
		})
	}
}

func buildSnapshotForTest(t *testing.T, cfg config.Config, files map[string]string) *Snapshot {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := ResolveRoot(DefaultRoot(dir), cfg.Repository.FollowSymlinks, nil)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	snap, err := BuildSnapshot(context.Background(), root, cfg.Repository)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	return snap
}

func TestListFilesReturnsCopy(t *testing.T) {
	snap := buildSnapshotForTest(t, config.Defaults(), map[string]string{
		"a.go": "package a\n", "b.go": "package b\n", "c.go": "package c\n",
	})
	first := snap.ListFiles()
	if len(first) < 2 {
		t.Fatalf("expected multiple files, got %d", len(first))
	}
	// Mutating the returned slice must not affect the snapshot's internal order,
	// which the trigram index relies on (file IDs are indices into ListFiles).
	first[0], first[len(first)-1] = first[len(first)-1], first[0]
	second := snap.ListFiles()
	for i := range second {
		if second[i].Path != snap.files[i].Path {
			t.Fatalf("ListFiles must return a copy; index %d diverged after caller mutation", i)
		}
	}
}

func TestReadFileBuildsOffsets(t *testing.T) {
	content := "package main\nfunc main() {}\n"
	snap := buildSnapshotForTest(t, config.Defaults(), map[string]string{"main.go": content})

	fc, err := snap.ReadFile(context.Background(), "main.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if fc.Lines != countLines([]byte(content)) {
		t.Errorf("Lines = %d, want %d", fc.Lines, countLines([]byte(content)))
	}
	want := LineOffsets([]byte(content))
	if len(fc.LineOffsets) != len(want) {
		t.Fatalf("LineOffsets len = %d, want %d", len(fc.LineOffsets), len(want))
	}
	for i := range want {
		if fc.LineOffsets[i] != want[i] {
			t.Fatalf("LineOffsets = %v, want %v", fc.LineOffsets, want)
		}
	}
}

func TestReadFileCacheStable(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	snap := buildSnapshotForTest(t, config.Defaults(), map[string]string{"main.go": content})
	ctx := context.Background()

	if _, err := snap.ReadFile(ctx, "main.go"); err != nil {
		t.Fatalf("first read: %v", err)
	}
	first := snap.contentsBytes
	if first != int64(len(content)) {
		t.Errorf("contentsBytes after first read = %d, want %d", first, len(content))
	}
	if _, err := snap.ReadFile(ctx, "main.go"); err != nil {
		t.Fatalf("second read: %v", err)
	}
	if snap.contentsBytes != first {
		t.Errorf("contentsBytes grew on cache hit: %d -> %d", first, snap.contentsBytes)
	}
}

func TestReadFileCacheCapNotExceeded(t *testing.T) {
	content := "this file is larger than the tiny cache budget\n"
	cfg := config.Defaults()
	cfg.Repository.MaxTotalSnapshotBytes = 4 // smaller than the file
	snap := buildSnapshotForTest(t, cfg, map[string]string{"main.go": content})

	fc, err := snap.ReadFile(context.Background(), "main.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(fc.Bytes) != content {
		t.Errorf("over-cap read returned wrong content: %q", string(fc.Bytes))
	}
	if snap.contentsBytes != 0 {
		t.Errorf("over-cap file was cached: contentsBytes = %d", snap.contentsBytes)
	}
	if len(snap.contents) != 0 {
		t.Errorf("over-cap file was cached: %d entries", len(snap.contents))
	}
}

func TestReadFileCacheAggregateCap(t *testing.T) {
	// Two equally sized files; the cache budget holds exactly one of them.
	fileA := "package a\n" + strings.Repeat("// a\n", 10)
	fileB := "package b\n" + strings.Repeat("// b\n", 10)
	if len(fileA) != len(fileB) {
		t.Fatalf("test setup: files must be equal size, got %d and %d", len(fileA), len(fileB))
	}
	cfg := config.Defaults()
	cfg.Repository.MaxTotalSnapshotBytes = int64(len(fileA))
	snap := buildSnapshotForTest(t, cfg, map[string]string{"a.go": fileA, "b.go": fileB})
	ctx := context.Background()

	fa, err := snap.ReadFile(ctx, "a.go")
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	fb, err := snap.ReadFile(ctx, "b.go")
	if err != nil {
		t.Fatalf("read b: %v", err)
	}
	if string(fa.Bytes) != fileA || string(fb.Bytes) != fileB {
		t.Error("aggregate-cap reads returned wrong content")
	}
	if snap.contentsBytes > cfg.Repository.MaxTotalSnapshotBytes {
		t.Errorf("contentsBytes %d exceeds cap %d", snap.contentsBytes, cfg.Repository.MaxTotalSnapshotBytes)
	}
	if len(snap.contents) != 1 {
		t.Errorf("expected exactly 1 cached entry under the cap, got %d", len(snap.contents))
	}
}

func TestReadFileConcurrentSingleAdmission(t *testing.T) {
	content := "package a\nvar A = 1\n"
	snap := buildSnapshotForTest(t, config.Defaults(), map[string]string{"a.go": content})
	ctx := context.Background()

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := snap.ReadFile(ctx, "a.go"); err != nil {
				t.Errorf("read: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	// The double-checked admit must charge the file once, not once per racer.
	if snap.contentsBytes != int64(len(content)) {
		t.Errorf("contentsBytes = %d, want %d (double-counted?)", snap.contentsBytes, len(content))
	}
	if len(snap.contents) != 1 {
		t.Errorf("cache entries = %d, want 1", len(snap.contents))
	}
}

func TestReadFileConcurrent(t *testing.T) {
	files := map[string]string{
		"a.go": "package a\nvar A = 1\n",
		"b.go": "package b\nvar B = 2\nvar C = 3\n",
		"c.go": "package c\n",
	}
	snap := buildSnapshotForTest(t, config.Defaults(), files)
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				for name, want := range files {
					fc, err := snap.ReadFile(ctx, name)
					if err != nil {
						t.Errorf("read %s: %v", name, err)
						return
					}
					if string(fc.Bytes) != want {
						t.Errorf("read %s = %q, want %q", name, string(fc.Bytes), want)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
