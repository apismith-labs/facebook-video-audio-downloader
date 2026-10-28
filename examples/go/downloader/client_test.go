package downloader

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedCall struct {
	method string
	path   string
	query  url.Values
}

type fakeTransport struct {
	mu      sync.Mutex
	calls   []recordedCall
	handler func(method, path string, call int) (any, error)
}

func (f *fakeTransport) Request(_ context.Context, method, path string, _ any, query url.Values) (any, error) {
	f.mu.Lock()
	f.calls = append(f.calls, recordedCall{method: method, path: path, query: query})
	count := len(f.calls)
	f.mu.Unlock()
	return f.handler(method, path, count)
}

func (f *fakeTransport) countMethod(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, call := range f.calls {
		if call.method == method {
			total++
		}
	}
	return total
}

func (f *fakeTransport) firstQuery(method string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.method == method {
			return call.query
		}
	}
	return nil
}

func createdRun() map[string]any {
	return map[string]any{"data": map[string]any{
		"id":               "run-1",
		"status":           "READY",
		"defaultDatasetId": "ds-1",
		"buildNumber":      ApprovedBuildNumber,
		"buildId":          ApprovedBuildID,
	}}
}

func succeededRun() map[string]any {
	return map[string]any{"data": map[string]any{
		"id":               "run-1",
		"status":           "SUCCEEDED",
		"defaultDatasetId": "ds-1",
		"buildNumber":      ApprovedBuildNumber,
		"buildId":          ApprovedBuildID,
	}}
}

func transcriptItem() map[string]any {
	return map[string]any{
		"url":        "https://www.facebook.com/watch/?v=1115932820735409",
		"title":      "Q & A about sleeping",
		"duration":   43.5,
		"nickname":   "Dr. Shu",
		"text":       " Hello, Dr.",
		"errMsg":     "",
		"likesCount": 75000,
		"shareCount": 1200,
		"segments": []any{
			map[string]any{"start": 0.0, "end": 0.68, "text": " Hello, Dr."},
			map[string]any{"start": 2.0, "end": 1.0, "text": "bad order"},
			map[string]any{"start": -1.0, "end": 3.0, "text": "negative"},
			map[string]any{"start": "x", "end": 3.0, "text": "not numeric"},
			map[string]any{"start": 3.0, "end": 4.0},
		},
	}
}

func happyTransport(items []any) *fakeTransport {
	return &fakeTransport{handler: func(method, path string, _ int) (any, error) {
		switch {
		case method == "POST":
			return createdRun(), nil
		case strings.HasPrefix(path, "/actor-runs/"):
			return succeededRun(), nil
		case strings.HasPrefix(path, "/datasets/"):
			return items, nil
		default:
			return nil, &ApiError{Message: "unexpected path " + path, Status: 404}
		}
	}}
}

func defaultOptions(transport Transport) Options {
	return Options{
		Token:       "test-token",
		Transport:   transport,
		Sleep:       func(time.Duration) {},
		MaxAttempts: 3,
		Timeout:     5 * time.Second,
	}
}

func TestGetTranscriptSuccess(t *testing.T) {
	items := []any{transcriptItem()}
	result, err := GetTranscript(context.Background(), "https://www.facebook.com/watch/?v=1234567890", defaultOptions(happyTransport(items)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "success" {
		t.Fatalf("status = %q, want success", result.Status)
	}
	if result.Text != " Hello, Dr." {
		t.Fatalf("text = %q", result.Text)
	}
	if len(result.Segments) != 1 {
		t.Fatalf("segments = %d, want 1 (invalid entries must be dropped)", len(result.Segments))
	}
	if result.RunID != "run-1" || result.DatasetID != "ds-1" {
		t.Fatalf("run/dataset = %q/%q", result.RunID, result.DatasetID)
	}
	if result.CanonicalURL != "https://www.facebook.com/watch/?v=1234567890" {
		t.Fatalf("canonical = %q", result.CanonicalURL)
	}
	if !result.DatasetComplete {
		t.Fatal("a short Dataset must be reported complete")
	}
	if !result.HasDuration || result.Duration != 43.5 {
		t.Fatalf("duration = %v (%v)", result.Duration, result.HasDuration)
	}
	if item := result.FirstItem(); item == nil || item["likesCount"] == nil {
		t.Fatal("raw items must keep undocumented fields")
	}
	if result.ExitCode() != 0 {
		t.Fatalf("exit code = %d", result.ExitCode())
	}
}

func TestGetTranscriptEmptyDataset(t *testing.T) {
	result, err := GetTranscript(context.Background(), "https://www.facebook.com/reel/1234567890/", defaultOptions(happyTransport([]any{})))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "empty" {
		t.Fatalf("status = %q, want empty", result.Status)
	}
	if result.ExitCode() != 5 {
		t.Fatalf("exit code = %d, want 5", result.ExitCode())
	}
}

func TestGetTranscriptActorErrorWins(t *testing.T) {
	item := transcriptItem()
	item["errMsg"] = "video is private"
	result, err := GetTranscript(context.Background(), "https://www.facebook.com/watch/?v=1234567890", defaultOptions(happyTransport([]any{item})))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "actor_error" {
		t.Fatalf("status = %q, want actor_error", result.Status)
	}
	if result.ExitCode() != 4 {
		t.Fatalf("exit code = %d, want 4", result.ExitCode())
	}
}

func TestGetTranscriptNoTranscript(t *testing.T) {
	item := transcriptItem()
	item["text"] = nil
	result, err := GetTranscript(context.Background(), "https://www.facebook.com/watch/?v=1234567890", defaultOptions(happyTransport([]any{item})))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "no_transcript" {
		t.Fatalf("status = %q, want no_transcript", result.Status)
	}
	if result.ExitCode() != 6 {
		t.Fatalf("exit code = %d, want 6", result.ExitCode())
	}
}

func TestGetTranscriptCreationUnknownOnServerError(t *testing.T) {
	transport := &fakeTransport{handler: func(method, path string, _ int) (any, error) {
		return nil, &ApiError{Message: "Apify HTTP 500", Status: 500, Ambiguous: true}
	}}
	_, err := GetTranscript(context.Background(), "https://www.facebook.com/watch/?v=1234567890", defaultOptions(transport))
	if _, ok := err.(*CreationUnknownError); !ok {
		t.Fatalf("error = %T, want CreationUnknownError", err)
	}
	if transport.countMethod("POST") != 1 {
		t.Fatalf("POST count = %d, want exactly 1", transport.countMethod("POST"))
	}
}

func TestGetTranscriptCreationRejectedIsTransportError(t *testing.T) {
	transport := &fakeTransport{handler: func(method, path string, _ int) (any, error) {
		return nil, &ApiError{Message: "Apify HTTP 400", Status: 400}
	}}
	_, err := GetTranscript(context.Background(), "https://www.facebook.com/watch/?v=1234567890", defaultOptions(transport))
	if _, ok := err.(*TransportError); !ok {
		t.Fatalf("error = %T, want TransportError", err)
	}
	if transport.countMethod("POST") != 1 {
		t.Fatalf("POST count = %d, want exactly 1", transport.countMethod("POST"))
	}
}

func TestGetTranscriptRateLimitedCreationIsNotRetried(t *testing.T) {
	transport := &fakeTransport{handler: func(method, path string, _ int) (any, error) {
		return nil, &ApiError{Message: "Apify HTTP 429", Status: 429}
	}}
	_, err := GetTranscript(context.Background(), "https://www.facebook.com/watch/?v=1234567890", defaultOptions(transport))
	if _, ok := err.(*CreationUnknownError); !ok {
		t.Fatalf("error = %T, want CreationUnknownError", err)
	}
	if transport.countMethod("POST") != 1 {
		t.Fatalf("POST count = %d, want exactly 1", transport.countMethod("POST"))
	}
}

func TestGetTranscriptRunFailed(t *testing.T) {
	transport := &fakeTransport{handler: func(method, path string, call int) (any, error) {
		switch {
		case method == "POST":
			return createdRun(), nil
		case strings.HasPrefix(path, "/actor-runs/"):
			return map[string]any{"data": map[string]any{
				"id": "run-1", "status": "FAILED", "defaultDatasetId": "ds-1",
				"buildNumber": ApprovedBuildNumber, "buildId": ApprovedBuildID,
			}}, nil
		default:
			return nil, &ApiError{Message: "unexpected path", Status: 404}
		}
	}}
	result, err := GetTranscript(context.Background(), "https://www.facebook.com/watch/?v=1234567890", defaultOptions(transport))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "run_failed" {
		t.Fatalf("status = %q, want run_failed", result.Status)
	}
	if result.ExitCode() != 9 {
		t.Fatalf("exit code = %d, want 9", result.ExitCode())
	}
}

func TestGetTranscriptBuildMismatch(t *testing.T) {
	transport := &fakeTransport{handler: func(method, path string, _ int) (any, error) {
		if method == "POST" {
			return map[string]any{"data": map[string]any{
				"id": "run-1", "status": "READY", "defaultDatasetId": "ds-1",
				"buildNumber": "0.0.19", "buildId": "someOtherBuildId",
			}}, nil
		}
		return nil, &ApiError{Message: "unexpected path", Status: 404}
	}}
	_, err := GetTranscript(context.Background(), "https://www.facebook.com/watch/?v=1234567890", defaultOptions(transport))
	if _, ok := err.(*ProtocolError); !ok {
		t.Fatalf("error = %T, want ProtocolError", err)
	}
	if transport.countMethod("POST") != 1 {
		t.Fatalf("POST count = %d, want exactly 1", transport.countMethod("POST"))
	}
}

func TestGetTranscriptRetriesPolling(t *testing.T) {
	transport := &fakeTransport{handler: func(method, path string, _ int) (any, error) {
		switch {
		case method == "POST":
			return createdRun(), nil
		case strings.HasPrefix(path, "/actor-runs/"):
			return succeededRun(), nil
		case strings.HasPrefix(path, "/datasets/"):
			return []any{transcriptItem()}, nil
		default:
			return nil, &ApiError{Message: "unexpected path", Status: 404}
		}
	}}

	// Wrap the happy handler with two 503s on the first Run poll.
	inner := transport.handler
	failures := 0
	transport.handler = func(method, path string, call int) (any, error) {
		if method == "GET" && strings.HasPrefix(path, "/actor-runs/") && failures < 2 {
			failures++
			return nil, &ApiError{Message: "Apify HTTP 503", Status: 503}
		}
		return inner(method, path, call)
	}

	result, err := GetTranscript(context.Background(), "https://www.facebook.com/watch/?v=1234567890", defaultOptions(transport))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("status = %q, want success", result.Status)
	}
	if failures != 2 {
		t.Fatalf("failures = %d, want 2", failures)
	}
}

func TestGetTranscriptSendsPinnedBuildAndCap(t *testing.T) {
	transport := happyTransport([]any{transcriptItem()})
	options := defaultOptions(transport)
	options.MaxTotalChargeUSD = "0.10"

	if _, err := GetTranscript(context.Background(), "https://www.facebook.com/watch/?v=1234567890", options); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	query := transport.firstQuery("POST")
	if query.Get("build") != ApprovedBuildNumber {
		t.Fatalf("build = %q", query.Get("build"))
	}
	if query.Get("restartOnError") != "false" {
		t.Fatalf("restartOnError = %q", query.Get("restartOnError"))
	}
	if query.Get("maxTotalChargeUsd") != "0.1" {
		t.Fatalf("maxTotalChargeUsd = %q", query.Get("maxTotalChargeUsd"))
	}
}

func TestGetTranscriptRejectsBadInputBeforeAnyRequest(t *testing.T) {
	transport := happyTransport([]any{transcriptItem()})
	_, err := GetTranscript(context.Background(), "http://www.facebook.com/watch/?v=1", defaultOptions(transport))
	if _, ok := err.(*ConfigurationError); !ok {
		t.Fatalf("error = %T, want ConfigurationError", err)
	}
	if len(transport.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(transport.calls))
	}
}
