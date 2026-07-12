package logrotate

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRotateDir_TruncatesOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.log")

	var sb strings.Builder
	const totalLines = 30000
	for i := 0; i < totalLines; i++ {
		sb.WriteString("line " + strconv.Itoa(i) + " padding padding padding padding\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Size() <= MaxSizeBytes {
		t.Fatalf("test setup bug: file must exceed MaxSizeBytes, got %d", info.Size())
	}

	rotated, err := RotateDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rotated) != 1 || rotated[0] != "big.log" {
		t.Fatalf("expected big.log reported as rotated, got %v", rotated)
	}

	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != KeepLines {
		t.Fatalf("expected exactly %d lines kept, got %d", KeepLines, len(lines))
	}
	if lines[len(lines)-1] != "line 29999 padding padding padding padding" {
		t.Fatalf("expected the last line preserved, got %q", lines[len(lines)-1])
	}
}

func TestRotateDir_LeavesSmallFilesAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.log")
	os.WriteFile(path, []byte("just a few lines\n"), 0644)

	rotated, err := RotateDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rotated) != 0 {
		t.Fatalf("expected no files rotated, got %v", rotated)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "just a few lines\n" {
		t.Fatal("expected small file left untouched")
	}
}

func TestRotateDir_IgnoresNonLogFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	var sb strings.Builder
	for i := 0; i < 100000; i++ {
		sb.WriteString("x")
	}
	os.WriteFile(path, []byte(sb.String()), 0644)

	rotated, err := RotateDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rotated) != 0 {
		t.Fatalf("expected .txt files ignored, got %v", rotated)
	}
}
