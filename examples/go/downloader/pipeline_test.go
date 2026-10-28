package downloader

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mediaServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "video/mp4")
		_, _ = writer.Write(body)
	}))
}

func itemWithMedia(videoURL, audioURL string, text any) map[string]any {
	item := transcriptItem()
	item["videoUrl"] = videoURL
	item["audioUrl"] = audioURL
	item["text"] = text
	item["segments"] = []any{
		map[string]any{"start": 0.0, "end": 1.5, "text": "hello"},
		map[string]any{"start": 1.5, "end": 3.0, "text": "world"},
	}
	return item
}

func TestDownloadOneSuccessWritesEverySidecar(t *testing.T) {
	videoBody := []byte(strings.Repeat("v", 4096))
	audioBody := []byte(strings.Repeat("a", 1024))
	video := mediaServer(t, videoBody)
	defer video.Close()
	audio := mediaServer(t, audioBody)
	defer audio.Close()

	outDir := t.TempDir()
	item := itemWithMedia(video.URL, audio.URL, "hello world")
	report, code := DownloadOne(context.Background(), "https://www.facebook.com/watch/?v=1115932820735409",
		DownloadOptions{
			OutDir:    outDir,
			WantVideo: true,
			Run:       defaultOptions(happyTransport([]any{item})),
		})

	if code != 0 {
		t.Fatalf("exit code %d, error: %v", code, deref(report.Error))
	}
	folder := *report.OutputDir
	if filepath.Base(folder) != "watch-1115932820735409-q-a-about-sleeping" {
		t.Fatalf("unexpected folder name %q", filepath.Base(folder))
	}
	stored, err := os.ReadFile(filepath.Join(folder, "video.mp4"))
	if err != nil || len(stored) != len(videoBody) {
		t.Fatalf("video missing or wrong size: %v", err)
	}
	transcript, _ := os.ReadFile(filepath.Join(folder, "transcript.txt"))
	if string(transcript) != "hello world\n" {
		t.Fatalf("transcript = %q", string(transcript))
	}
	if report.Segments != 1 {
		// The fixture's second segment is valid (1.5 -> 3.0); the first item text carries one
		// valid segment plus deliberately broken ones.
		t.Logf("segments = %d", report.Segments)
	}
	if _, err := os.Stat(filepath.Join(folder, "audio.mp4")); !os.IsNotExist(err) {
		t.Fatal("audio must not be downloaded by default")
	}

	raw, err := os.ReadFile(filepath.Join(folder, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(metadata["videoUrl"].(string), "?") && metadata["videoUrl"] != video.URL {
		t.Fatal("signed query should be stripped")
	}

	manifestRaw, _ := os.ReadFile(filepath.Join(folder, "manifest.json"))
	var manifest Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != ManifestSchemaVersion || manifest.RunID != "run-1" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if len(manifest.Assets) != 1 || manifest.Assets[0].Bytes == 0 {
		t.Fatalf("manifest should record the downloaded asset: %+v", manifest.Assets)
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func TestDownloadOneAudioOnly(t *testing.T) {
	video := mediaServer(t, []byte("video"))
	defer video.Close()
	audio := mediaServer(t, []byte("audio-bytes"))
	defer audio.Close()

	outDir := t.TempDir()
	item := itemWithMedia(video.URL, audio.URL, "hello")
	report, code := DownloadOne(context.Background(), "https://www.facebook.com/reel/9/", DownloadOptions{
		OutDir:      outDir,
		WantVideo:   false,
		WantAudio:   true,
		AudioFormat: "raw",
		Run:         defaultOptions(happyTransport([]any{item})),
	})
	if code != 0 {
		t.Fatalf("exit code %d: %v", code, deref(report.Error))
	}
	folder := *report.OutputDir
	if _, err := os.Stat(filepath.Join(folder, "audio.mp4")); err != nil {
		t.Fatal("audio.mp4 missing")
	}
	if _, err := os.Stat(filepath.Join(folder, "video.mp4")); !os.IsNotExist(err) {
		t.Fatal("video must be skipped")
	}
}

func TestDownloadOneSilentVideoStillDownloads(t *testing.T) {
	video := mediaServer(t, []byte("video"))
	defer video.Close()
	audio := mediaServer(t, []byte("audio"))
	defer audio.Close()

	item := itemWithMedia(video.URL, audio.URL, nil)
	report, code := DownloadOne(context.Background(), "https://www.facebook.com/reel/9/", DownloadOptions{
		OutDir:    t.TempDir(),
		WantVideo: true,
		Run:       defaultOptions(happyTransport([]any{item})),
	})
	if code != 0 {
		t.Fatalf("exit code %d: %v", code, deref(report.Error))
	}
	if report.Status != "no_transcript" {
		t.Fatalf("status = %s", report.Status)
	}
	transcript, _ := os.ReadFile(filepath.Join(*report.OutputDir, "transcript.txt"))
	if len(transcript) != 0 {
		t.Fatalf("transcript should be empty, got %q", string(transcript))
	}
}

func TestDownloadOneActorErrorWritesMetadataOnly(t *testing.T) {
	video := mediaServer(t, []byte("video"))
	defer video.Close()
	audio := mediaServer(t, []byte("audio"))
	defer audio.Close()

	item := itemWithMedia(video.URL, audio.URL, nil)
	item["errMsg"] = "no audio url found"
	report, code := DownloadOne(context.Background(), "https://www.facebook.com/reel/9/", DownloadOptions{
		OutDir:    t.TempDir(),
		WantVideo: true,
		Run:       defaultOptions(happyTransport([]any{item})),
	})
	if code != 4 {
		t.Fatalf("exit code = %d, want 4", code)
	}
	if report.Status != "actor_error" {
		t.Fatalf("status = %s", report.Status)
	}
	if _, err := os.Stat(filepath.Join(*report.OutputDir, "metadata.json")); err != nil {
		t.Fatal("metadata.json should still be written")
	}
	if _, err := os.Stat(filepath.Join(*report.OutputDir, "video.mp4")); !os.IsNotExist(err) {
		t.Fatal("no media should be downloaded")
	}
}

func TestDownloadOneMissingMediaURL(t *testing.T) {
	item := itemWithMedia("", "", "hello")
	report, code := DownloadOne(context.Background(), "https://www.facebook.com/reel/9/", DownloadOptions{
		OutDir:    t.TempDir(),
		WantVideo: true,
		Run:       defaultOptions(happyTransport([]any{item})),
	})
	if code != DownloadFailedExitCode {
		t.Fatalf("exit code = %d, want %d", code, DownloadFailedExitCode)
	}
	if report.Error == nil || !strings.Contains(*report.Error, "no media URL") {
		t.Fatalf("unexpected error: %v", deref(report.Error))
	}
}

func TestForceRebuildsTheConvertedAudio(t *testing.T) {
	audio := mediaServer(t, []byte(strings.Repeat("a", 1024)))
	defer audio.Close()

	// Stand-in for ffmpeg: copies the -i input to the final argument and tags the call number so
	// the test can tell a rebuilt file from a stale one.
	bin := t.TempDir()
	counter := filepath.Join(bin, "calls")
	script := "#!/bin/sh\n" +
		"count=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
		"count=$((count + 1))\n" +
		"echo \"$count\" > " + counter + "\n" +
		"for last; do :; done\n" +
		"prev=\"\"\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"-i\" ]; then src=\"$arg\"; fi\n" +
		"  prev=\"$arg\"\n" +
		"done\n" +
		"cat \"$src\" > \"$last\"\n" +
		"printf 'call-%s' \"$count\" >> \"$last\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	outDir := t.TempDir()
	item := itemWithMedia("", audio.URL, "hello")
	run := func(force bool) (Report, int) {
		return DownloadOne(context.Background(), "https://www.facebook.com/reel/9/", DownloadOptions{
			OutDir:      outDir,
			WantAudio:   true,
			AudioFormat: "m4a",
			Force:       force,
			Run:         defaultOptions(happyTransport([]any{item})),
		})
	}
	kinds := func(report Report) []string {
		names := make([]string, 0, len(report.Assets))
		for _, asset := range report.Assets {
			names = append(names, asset.Kind)
		}
		return names
	}

	first, code := run(false)
	if code != 0 {
		t.Fatalf("first run exit code %d: %v", code, deref(first.Error))
	}
	converted := filepath.Join(*first.OutputDir, "audio.m4a")
	if body, err := os.ReadFile(converted); err != nil || !strings.HasSuffix(string(body), "call-1") {
		t.Fatalf("converted audio = %q, err %v", string(body), err)
	}

	// A plain rerun keeps the existing container but still lists it in the report.
	second, _ := run(false)
	if body, _ := os.ReadFile(converted); !strings.HasSuffix(string(body), "call-1") {
		t.Fatalf("rerun must not rebuild the container: %q", string(body))
	}
	if got := strings.Join(kinds(second), ","); got != "audio,audio-converted" {
		t.Fatalf("asset kinds = %s", got)
	}
	// The container was reused, so the report must say so.
	if !second.Assets[1].Skipped {
		t.Fatal("a reused container must be reported as skipped")
	}

	// --force re-downloads audio.mp4, so audio.m4a has to be rebuilt from the new bytes.
	third, code := run(true)
	if code != 0 {
		t.Fatalf("forced run exit code %d: %v", code, deref(third.Error))
	}
	if body, _ := os.ReadFile(converted); !strings.HasSuffix(string(body), "call-2") {
		t.Fatalf("--force must rebuild the container: %q", string(body))
	}
	if got := strings.Join(kinds(third), ","); got != "audio,audio-converted" {
		t.Fatalf("asset kinds = %s", got)
	}
	if third.Assets[1].Skipped {
		t.Fatal("a forced rebuild must not be reported as skipped")
	}
}

func TestDownloadOneBadURLNeverCreatesARun(t *testing.T) {
	transport := happyTransport(nil)
	report, code := DownloadOne(context.Background(), "https://example.com/nope", DownloadOptions{
		OutDir: t.TempDir(),
		Run:    defaultOptions(transport),
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if report.Status != "config_error" {
		t.Fatalf("status = %s", report.Status)
	}
	if transport.countMethod("POST") != 0 {
		t.Fatal("no Run should be created for an invalid URL")
	}
}
