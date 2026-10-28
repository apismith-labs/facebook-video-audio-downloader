package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func payload(size int) []byte {
	body := make([]byte, size)
	for index := range body {
		body[index] = byte(index % 256)
	}
	return body
}

func rangeServer(t *testing.T, body []byte, allowRange bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		start := 0
		if allowRange {
			if header := request.Header.Get("Range"); strings.HasPrefix(header, "bytes=") {
				value := strings.TrimSuffix(strings.TrimPrefix(header, "bytes="), "-")
				if parsed, err := strconv.Atoi(value); err == nil {
					start = parsed
				}
			}
		}
		if start >= len(body) {
			writer.Header().Set("Content-Range", "bytes */"+strconv.Itoa(len(body)))
			writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		chunk := body[start:]
		writer.Header().Set("Content-Type", "video/mp4")
		if start > 0 {
			writer.Header().Set("Content-Range", "bytes "+strconv.Itoa(start)+"-"+strconv.Itoa(len(body)-1)+"/"+strconv.Itoa(len(body)))
			writer.WriteHeader(http.StatusPartialContent)
		}
		_, _ = writer.Write(chunk)
	}))
}

func TestDownloadMediaFullFile(t *testing.T) {
	body := payload(8192)
	server := rangeServer(t, body, true)
	defer server.Close()

	target := filepath.Join(t.TempDir(), "video.mp4")
	asset, err := DownloadMedia(context.Background(), server.URL, target, MediaOptions{})
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	expected := sha256.Sum256(body)
	if asset.SHA256 != hex.EncodeToString(expected[:]) {
		t.Fatal("sha256 mismatch")
	}
	if asset.Bytes != int64(len(body)) {
		t.Fatalf("bytes = %d, want %d", asset.Bytes, len(body))
	}
	if _, err := os.Stat(target + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part should be renamed away")
	}
}

func TestDownloadMediaResumesWithRange(t *testing.T) {
	body := payload(8192)
	server := rangeServer(t, body, true)
	defer server.Close()

	target := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(target+".part", body[:3000], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DownloadMedia(context.Background(), server.URL, target, MediaOptions{}); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	stored, _ := os.ReadFile(target)
	if string(stored) != string(body) {
		t.Fatal("resumed file does not match")
	}
}

func TestDownloadMediaRestartsWhenRangeIgnored(t *testing.T) {
	body := payload(4096)
	server := rangeServer(t, body, false)
	defer server.Close()

	target := filepath.Join(t.TempDir(), "video.mp4")
	_ = os.WriteFile(target+".part", body[:1000], 0o644)
	if _, err := DownloadMedia(context.Background(), server.URL, target, MediaOptions{}); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	stored, _ := os.ReadFile(target)
	if len(stored) != len(body) {
		t.Fatalf("bytes = %d, want %d", len(stored), len(body))
	}
}

func TestDownloadMediaSkipsExistingFile(t *testing.T) {
	body := payload(1024)
	target := filepath.Join(t.TempDir(), "video.mp4")
	_ = os.WriteFile(target, body, 0o644)

	asset, err := DownloadMedia(context.Background(), "http://127.0.0.1:9/never.mp4", target, MediaOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !asset.Skipped {
		t.Fatal("expected skipped")
	}
}

func TestDownloadMediaDoesNotRetryForbidden(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		writer.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "video.mp4")
	_, err := DownloadMedia(context.Background(), server.URL, target, MediaOptions{
		Sleep: func(time.Duration) {},
	})
	var mediaErr *MediaError
	if err == nil || !errors.As(err, &mediaErr) || mediaErr.Status != 403 {
		t.Fatalf("expected a 403 MediaError, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestDownloadMediaRetriesTransientFailure(t *testing.T) {
	body := payload(2048)
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "video/mp4")
		_, _ = writer.Write(body)
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "video.mp4")
	asset, err := DownloadMedia(context.Background(), server.URL, target, MediaOptions{
		Sleep: func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("expected retry to succeed: %v", err)
	}
	if asset.Bytes != int64(len(body)) {
		t.Fatalf("bytes = %d", asset.Bytes)
	}
}

func TestRedactMediaURL(t *testing.T) {
	got := RedactMediaURL("https://scontent.xx.fbcdn.net/v/a.mp4?oh=1&oe=2&sig=3")
	if got != "https://scontent.xx.fbcdn.net/v/a.mp4" {
		t.Fatalf("got %q", got)
	}
	if RedactMediaURL("nonsense") != "[unparseable media url]" {
		t.Fatal("expected unparseable marker")
	}
	if MediaHost("https://scontent.xx.fbcdn.net/v/a.mp4") != "scontent.xx.fbcdn.net" {
		t.Fatal("host mismatch")
	}
}

func TestAudioTargetAndArgs(t *testing.T) {
	if _, remux, _ := AudioTarget("out/audio.mp4", "raw"); remux {
		t.Fatal("raw must not remux")
	}
	if path, remux, _ := AudioTarget("out/audio.mp4", "m4a"); !remux || path != "out/audio.m4a" {
		t.Fatalf("unexpected target %q", path)
	}
	args, err := FfmpegArgs("a.mp4", "a.m4a", "m4a")
	if err != nil || !contains(args, "copy") {
		t.Fatal("m4a should copy the stream")
	}
	args, _ = FfmpegArgs("a.mp4", "a.mp3", "mp3")
	if !contains(args, "libmp3lame") {
		t.Fatal("mp3 should re-encode")
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
