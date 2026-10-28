package downloader

import (
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"A Day at the Beach!": "a-day-at-the-beach",
		"海边的一天":               "",
		"Trip 2026 — 海边":      "trip-2026",
		// Digits separated by CJK collapse, matching the Python/Node/Java clients; a dash would
		// fork the folder name between clients.
		"韓國瑜 稱有人想當美國第51州、日本第5大島、中國第31省": "51531",
		"第51州、日本第5大島、中國第31省":            "51531",
	}
	for input, expected := range cases {
		if got := Slugify(input, 0); got != expected {
			t.Fatalf("Slugify(%q) = %q, want %q", input, got, expected)
		}
	}
	if got := Slugify("a", 10); got != "a" {
		t.Fatalf("short slug changed: %q", got)
	}
}

func TestVideoIdentity(t *testing.T) {
	cases := []struct{ url, form, want string }{
		{"https://www.facebook.com/watch/?v=123", "watch", "watch-123"},
		{"https://www.facebook.com/video.php?v=123", "video_php", "video-123"},
		{"https://www.facebook.com/PageName/videos/123/", "page_video", "page-pagename-123"},
		{"https://www.facebook.com/videos/123/", "video", "video-123"},
		{"https://www.facebook.com/reel/123/", "reel", "reel-123"},
		{"https://www.facebook.com/share/r/AbC-1/", "share", "share-r-AbC-1"},
		{"https://fb.watch/AbC123/", "short_link", "short-AbC123"},
	}
	for _, current := range cases {
		if got := VideoIdentity(current.url, current.form); got != current.want {
			t.Fatalf("VideoIdentity(%q) = %q, want %q", current.url, got, current.want)
		}
	}
}

func TestFolderName(t *testing.T) {
	got := FolderName("https://www.facebook.com/reel/9/", "reel", "Sunset Timelapse")
	if got != "reel-9-sunset-timelapse" {
		t.Fatalf("got %q", got)
	}
	if got := FolderName("https://www.facebook.com/reel/9/", "reel", "海边"); got != "reel-9" {
		t.Fatalf("non-ascii title should not add a suffix, got %q", got)
	}
}

func TestChooseOutputDir(t *testing.T) {
	base := t.TempDir()
	if got := ChooseOutputDir(base, "reel-1", "https://x/1"); got != base+"/reel-1" {
		t.Fatalf("got %q", got)
	}
	existing := base + "/reel-1"
	if _, err := WriteJSON(existing+"/manifest.json", map[string]any{"canonicalUrl": "https://x/other"}); err != nil {
		t.Fatal(err)
	}
	if got := ChooseOutputDir(base, "reel-1", "https://x/1"); got != base+"/reel-1-2" {
		t.Fatalf("expected a suffix, got %q", got)
	}
	if got := ChooseOutputDir(base, "reel-1", "https://x/other"); got != existing {
		t.Fatalf("expected reuse, got %q", got)
	}
}

func TestSanitizeItem(t *testing.T) {
	item := map[string]any{
		"videoUrl": "https://scontent.xx.fbcdn.net/v/a.mp4?oh=1&oe=2&sig=3",
		"audioUrl": "https://scontent.xx.fbcdn.net/v/b.mp4?oh=1",
		"text":     "hello",
	}
	cleaned := SanitizeItem(item, false)
	if cleaned["videoUrl"] != "https://scontent.xx.fbcdn.net/v/a.mp4" {
		t.Fatalf("videoUrl = %v", cleaned["videoUrl"])
	}
	if cleaned["text"] != "hello" {
		t.Fatal("unrelated fields must survive")
	}
	if item["videoUrl"] != "https://scontent.xx.fbcdn.net/v/a.mp4?oh=1&oe=2&sig=3" {
		t.Fatal("the source item must not be mutated")
	}
	kept := SanitizeItem(item, true)
	if kept["videoUrl"] != item["videoUrl"] {
		t.Fatal("opt-in should keep the signed URL")
	}
}

func TestTranscriptSegments(t *testing.T) {
	segments := TranscriptSegments([]map[string]any{
		{"start": 0.0, "end": 1.5, "text": "a"},
		{"start": 2.0, "end": 1.0, "text": "reversed"},
		{"start": "0", "end": 1.0, "text": "string start"},
		{"start": 1.0, "end": 2.0, "text": 5},
	})
	if len(segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(segments))
	}
	if segments[0]["text"] != "a" {
		t.Fatalf("unexpected segment: %v", segments[0])
	}
}
