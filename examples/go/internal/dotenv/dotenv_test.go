package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsTheNearestFileAndKeepsExistingValues(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "examples", "go")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "# comment\nAPIFY_TOKEN=from-file\nAPIFY_TEST_KEEP=ignored\nNOT_APIFY=skipped\n"
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(previous) }()

	t.Setenv("APIFY_SKIP_DOTENV", "")
	t.Setenv("APIFY_TOKEN", "")
	if err := os.Unsetenv("APIFY_TOKEN"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APIFY_TEST_KEEP", "keep")

	Load()

	if got := os.Getenv("APIFY_TOKEN"); got != "from-file" {
		t.Fatalf("APIFY_TOKEN = %q, want from-file", got)
	}
	if got := os.Getenv("APIFY_TEST_KEEP"); got != "keep" {
		t.Fatalf("an existing value must win, got %q", got)
	}
	if _, exists := os.LookupEnv("NOT_APIFY"); exists {
		t.Fatal("only APIFY_* keys are loaded")
	}
}

func TestLoadHonoursTheSkipSwitch(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("APIFY_SKIP_TEST=loaded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(previous) }()

	t.Setenv("APIFY_SKIP_DOTENV", "1")
	_ = os.Unsetenv("APIFY_SKIP_TEST")

	Load()

	if _, exists := os.LookupEnv("APIFY_SKIP_TEST"); exists {
		t.Fatal("APIFY_SKIP_DOTENV must disable the file")
	}
}
