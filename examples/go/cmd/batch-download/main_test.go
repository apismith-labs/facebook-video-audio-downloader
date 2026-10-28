package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCSV(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "videos.csv")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseRowsStripsAByteOrderMark(t *testing.T) {
	// Spreadsheet exports prefix the first header with a BOM; the other three clients strip it.
	path := writeCSV(t, "\ufeffvideoUrl,durationSeconds\nhttps://www.facebook.com/watch/?v=1131784255863745,42.261\n")
	rows, err := parseRows(path)
	if err != nil {
		t.Fatalf("parseRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].URLForm != "watch" {
		t.Fatalf("url form = %q", rows[0].URLForm)
	}
	if rows[0].DurationSeconds == nil || *rows[0].DurationSeconds != 42.261 {
		t.Fatalf("duration = %v", rows[0].DurationSeconds)
	}
}

func TestParseRowsAcceptsAUrlColumn(t *testing.T) {
	path := writeCSV(t, "url\nhttps://www.facebook.com/reel/1612151150942907\n")
	rows, err := parseRows(path)
	if err != nil {
		t.Fatalf("parseRows: %v", err)
	}
	if len(rows) != 1 || rows[0].URLForm != "reel" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestParseRowsRejectsAFileWithoutAUrlColumn(t *testing.T) {
	path := writeCSV(t, "link\nhttps://www.facebook.com/reel/1\n")
	if _, err := parseRows(path); err == nil {
		t.Fatal("a file without videoUrl/url must be rejected")
	}
}
