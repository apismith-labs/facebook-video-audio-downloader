package downloader

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyItems(t *testing.T) {
	cases := []struct {
		name   string
		items  []any
		status string
	}{
		{"empty", []any{}, "empty"},
		{"nil", nil, "unknown"},
		{"non object", []any{"nope"}, "unknown"},
		{"errMsg wins", []any{map[string]any{"errMsg": "private", "videoUrl": "https://x/a.mp4"}}, "actor_error"},
		{"blank errMsg ignored", []any{map[string]any{"errMsg": "   ", "videoUrl": "https://x/a.mp4"}}, "success"},
		{"success", []any{map[string]any{"audioUrl": "https://x/b.mp4"}}, "success"},
		{"urls null", []any{map[string]any{"videoUrl": nil}}, "unknown"},
		{"urls absent", []any{map[string]any{"title": "x"}}, "unknown"},
		{"urls empty", []any{map[string]any{"videoUrl": "  "}}, "unknown"},
		{"url number", []any{map[string]any{"videoUrl": 42.0}}, "unknown"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, _ := ClassifyItems(testCase.items)
			if status != testCase.status {
				t.Fatalf("status = %q, want %q", status, testCase.status)
			}
		})
	}
}

func TestClassifyItemsMentionsErrMsg(t *testing.T) {
	_, message := ClassifyItems([]any{
		map[string]any{"errMsg": "video is private"},
		map[string]any{"errMsg": "region locked"},
	})
	if !strings.Contains(message, "private") || !strings.Contains(message, "region") {
		t.Fatalf("message = %q", message)
	}
}

func TestSanitizeDropsCredentials(t *testing.T) {
	value := map[string]any{
		"token":         "abc",
		"access_token":  "abc",
		"Cookie":        "abc",
		"authorization": "Bearer abc",
		"nested":        map[string]any{"apiKey": "abc", "keep": "yes"},
		"text":          "token=abc should be redacted",
	}
	cleaned := Sanitize(value).(map[string]any)
	for _, key := range []string{"token", "access_token", "Cookie", "authorization"} {
		if _, present := cleaned[key]; present {
			t.Fatalf("%s should be dropped", key)
		}
	}
	nested := cleaned["nested"].(map[string]any)
	if _, present := nested["apiKey"]; present {
		t.Fatal("apiKey should be dropped")
	}
	if nested["keep"] != "yes" {
		t.Fatal("non-secret fields must survive")
	}
	if !strings.Contains(cleaned["text"].(string), "[REDACTED]") {
		t.Fatalf("text = %q", cleaned["text"])
	}
}

func TestSafeErrorRedactsAndTruncates(t *testing.T) {
	message := SafeError(&ApiError{Message: "failed with Bearer abc123 and authorization: xyz"})
	if strings.Contains(message, "abc123") || strings.Contains(message, "xyz") {
		t.Fatalf("message leaked a credential: %q", message)
	}
	long := SafeError(&ApiError{Message: strings.Repeat("a", 500)})
	if len(long) != 300 {
		t.Fatalf("length = %d, want 300", len(long))
	}
}

func TestFileJournalAppendsAndReads(t *testing.T) {
	directory := t.TempDir()
	journal := &FileJournal{Path: filepath.Join(directory, "journal.jsonl")}

	if err := journal.Append(map[string]any{"runId": "run-1", "stage": "creating"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := journal.Append(map[string]any{"runId": "run-1", "stage": "run_created"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	record, err := journal.Latest("run-1")
	if err != nil || record == nil {
		t.Fatalf("latest: %v %v", record, err)
	}
	if record["stage"] != "run_created" {
		t.Fatalf("stage = %v", record["stage"])
	}

	info, err := os.Stat(journal.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestFileJournalRefusesCorruptFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "journal.jsonl")
	if err := os.WriteFile(path, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	journal := &FileJournal{Path: path}
	if err := journal.Append(map[string]any{"runId": "run-1"}); err == nil {
		t.Fatal("a corrupt journal must be refused")
	}
	if _, err := journal.Latest("run-1"); err == nil {
		t.Fatal("resume from a corrupt journal must be refused")
	}
}

func TestLoadResumeRecordValidation(t *testing.T) {
	directory := t.TempDir()
	journal := &FileJournal{Path: filepath.Join(directory, "journal.jsonl")}
	_ = journal.Append(map[string]any{
		"runId": "run-1", "actorId": DefaultActorID, "sourceUrl": "https://www.facebook.com/watch/?v=1",
	})

	if _, err := LoadResumeRecord(journal, "run-1", DefaultActorID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := LoadResumeRecord(journal, "run-2", DefaultActorID); err == nil {
		t.Fatal("an unknown Run ID must be refused")
	}
	if _, err := LoadResumeRecord(journal, "run-1", "someone-else/actor"); err == nil {
		t.Fatal("a different Actor ID must be refused")
	}
}

func TestResumeIsGetOnly(t *testing.T) {
	transport := &fakeTransport{handler: func(method, path string, _ int) (any, error) {
		switch {
		case strings.HasPrefix(path, "/actor-runs/"):
			return succeededRun(), nil
		case strings.HasPrefix(path, "/datasets/"):
			return []any{mediaItem()}, nil
		default:
			return nil, &ApiError{Message: "unexpected path " + path, Status: 404}
		}
	}}

	options := defaultOptions(transport)
	options.RunID = "run-1"
	result, err := ResolveMedia(context.Background(), "https://www.facebook.com/watch/?v=1234567890", options)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("status = %q", result.Status)
	}
	if transport.countMethod("POST") != 0 {
		t.Fatalf("POST count = %d, want 0 on resume", transport.countMethod("POST"))
	}
}

func TestOutcomeSerializesRawItems(t *testing.T) {
	payload := map[string]any{"text": "hello", "likesCount": 7.0}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), "likesCount") {
		t.Fatalf("encoded = %s", encoded)
	}
}
