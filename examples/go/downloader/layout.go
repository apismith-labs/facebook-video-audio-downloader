package downloader

// Output layout: deterministic folder names and the sidecar files written next to the media.
// Every language client produces the same layout so a folder started by one client can be reused
// or inspected by another.

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	transcriptFile = "transcript.txt"
	segmentsFile   = "segments.json"
	metadataFile   = "metadata.json"
	manifestFile   = "manifest.json"
)

var (
	pageVideoPattern = regexp.MustCompile(`^/([A-Za-z0-9._-]+)/videos/(\d+)/?$`)
	videosPattern    = regexp.MustCompile(`^/videos/(\d+)/?$`)
	reelPattern      = regexp.MustCompile(`^/(?i:reel)/(\d+)/?$`)
	sharePattern     = regexp.MustCompile(`^/share/([A-Za-z]+)/([A-Za-z0-9_-]+)/?$`)
	shortPattern     = regexp.MustCompile(`^/([A-Za-z0-9_-]+)/?$`)
)

// Slugify returns an ASCII-only, lower-case, dash-separated slug. Non-ASCII titles degrade to "".
//
// Non-ASCII characters are dropped without creating a word boundary, which is what the Python,
// Node, and Java clients do after NFKD folding: the digits in "第51州、日本第5大島" collapse into
// "51531", not "51-5". Keeping the rule identical matters because the folder name is shared state
// between clients.
func Slugify(value string, limit int) string {
	if value == "" {
		return ""
	}
	if limit <= 0 {
		limit = mediaTitleLimit
	}
	var builder strings.Builder
	pendingDash := false
	for _, r := range value {
		if r > unicode.MaxASCII {
			// The other clients fold accented Latin letters to their base letter; without a
			// normalisation dependency this client drops them (documented in compatibility.md).
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			if builder.Len() > 0 {
				pendingDash = true
			}
			continue
		}
		if pendingDash {
			builder.WriteByte('-')
			pendingDash = false
		}
		builder.WriteRune(unicode.ToLower(r))
	}
	slug := strings.Trim(builder.String(), "-")
	if len(slug) > limit {
		slug = strings.TrimRight(slug[:limit], "-")
	}
	return slug
}

// VideoIdentity derives a stable folder identity from the canonical URL.
func VideoIdentity(canonicalURL, urlForm string) string {
	parsed, err := url.Parse(canonicalURL)
	if err != nil {
		if urlForm == "" {
			urlForm = "video"
		}
		return urlForm + "-unknown"
	}
	path := parsed.Path
	if urlForm == "watch" || urlForm == "video_php" {
		prefix := "watch"
		if urlForm == "video_php" {
			prefix = "video"
		}
		if id := parsed.Query().Get("v"); id != "" {
			if _, convErr := strconv.Atoi(id); convErr == nil {
				return prefix + "-" + id
			}
		}
		return prefix + "-unknown"
	}
	if match := pageVideoPattern.FindStringSubmatch(path); match != nil && urlForm == "page_video" {
		return "page-" + Slugify(match[1], 24) + "-" + match[2]
	}
	if match := videosPattern.FindStringSubmatch(path); match != nil {
		return "video-" + match[1]
	}
	if match := reelPattern.FindStringSubmatch(path); match != nil {
		return "reel-" + match[1]
	}
	if match := sharePattern.FindStringSubmatch(path); match != nil {
		return "share-" + strings.ToLower(match[1]) + "-" + match[2]
	}
	if urlForm == "short_link" {
		if match := shortPattern.FindStringSubmatch(path); match != nil {
			return "short-" + match[1]
		}
	}
	if urlForm == "" {
		urlForm = "video"
	}
	return urlForm + "-unknown"
}

// FolderName is the identity plus an optional readable title suffix.
func FolderName(canonicalURL, urlForm, title string) string {
	identity := VideoIdentity(canonicalURL, urlForm)
	if suffix := Slugify(title, mediaTitleLimit); suffix != "" {
		return identity + "-" + suffix
	}
	return identity
}

// ChooseOutputDir reuses an existing folder for the same URL and never overwrites another video.
func ChooseOutputDir(base, folder, canonicalURL string) string {
	candidate := filepath.Join(base, folder)
	for index := 2; ; index++ {
		raw, err := os.ReadFile(filepath.Join(candidate, manifestFile))
		if err != nil {
			return candidate
		}
		var manifest struct {
			CanonicalURL string `json:"canonicalUrl"`
		}
		if json.Unmarshal(raw, &manifest) != nil || manifest.CanonicalURL == "" ||
			manifest.CanonicalURL == canonicalURL {
			return candidate
		}
		candidate = filepath.Join(base, folder+"-"+strconv.Itoa(index))
	}
}

// SanitizeItem copies an Actor item, reducing signed media URLs to scheme://host/path.
func SanitizeItem(item map[string]any, keepMediaURLs bool) map[string]any {
	cleaned := map[string]any{}
	for key, value := range item {
		cleaned[key] = value
	}
	if keepMediaURLs {
		return cleaned
	}
	for _, field := range []string{"videoUrl", "audioUrl", "avatarUri"} {
		if raw, ok := cleaned[field].(string); ok && raw != "" {
			if redacted := RedactMediaURL(raw); !strings.HasPrefix(redacted, "[") {
				cleaned[field] = redacted
			}
		}
	}
	return cleaned
}

// TranscriptSegments keeps only well-formed segments.
func TranscriptSegments(segments []map[string]any) []map[string]any {
	result := []map[string]any{}
	for _, entry := range segments {
		start, startOK := numberValue(entry["start"])
		end, endOK := numberValue(entry["end"])
		text, textOK := entry["text"].(string)
		if !startOK || !endOK || !textOK || start < 0 || end < start {
			continue
		}
		result = append(result, map[string]any{"start": start, "end": end, "text": text})
	}
	return result
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

// WriteText writes UTF-8 text atomically and returns the byte count.
func WriteText(target, text string) (int, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, []byte(text), 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(temporary, target); err != nil {
		return 0, err
	}
	return len([]byte(text)), nil
}

// WriteJSON writes indented JSON atomically and returns the byte count.
func WriteJSON(target string, payload any) (int, error) {
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return 0, err
	}
	return WriteText(target, string(encoded)+"\n")
}

// Manifest is the machine-readable record written next to the media.
type Manifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	SourceURL     string          `json:"sourceUrl"`
	CanonicalURL  string          `json:"canonicalUrl"`
	URLForm       string          `json:"urlForm"`
	Status        string          `json:"status"`
	RunID         string          `json:"runId"`
	DatasetID     string          `json:"datasetId"`
	Title         string          `json:"title"`
	Nickname      string          `json:"nickname"`
	Duration      *float64        `json:"duration"`
	DownloadedAt  string          `json:"downloadedAt"`
	Assets        []MediaAsset    `json:"assets"`
	Transcript    TranscriptStats `json:"transcript"`
	MediaSources  []MediaSource   `json:"mediaSources"`
	Notes         []string        `json:"notes"`
}

// TranscriptStats records what the sidecar transcript files contain.
type TranscriptStats struct {
	Bytes    int `json:"bytes"`
	Segments int `json:"segments"`
}

// MediaSource records where a file came from, without the signature.
type MediaSource struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// NewManifest assembles a manifest; no signed URL is ever included.
func NewManifest(result Result, assets []MediaAsset, transcriptBytes, segmentCount int,
	sources []MediaSource) Manifest {
	var duration *float64
	if result.HasDuration {
		value := result.Duration
		duration = &value
	}
	return Manifest{
		SchemaVersion: ManifestSchemaVersion,
		SourceURL:     result.SourceURL,
		CanonicalURL:  result.CanonicalURL,
		URLForm:       result.URLForm,
		Status:        result.Status,
		RunID:         result.RunID,
		DatasetID:     result.DatasetID,
		Title:         result.Title,
		Nickname:      result.Nickname,
		Duration:      duration,
		DownloadedAt:  time.Now().UTC().Format(time.RFC3339),
		Assets:        assets,
		Transcript:    TranscriptStats{Bytes: transcriptBytes, Segments: segmentCount},
		MediaSources:  sources,
		Notes: []string{
			"Media links returned by the Actor are signed and short-lived; they are not stored here.",
			"Re-running the same URL reuses this folder and skips files whose size still matches.",
		},
	}
}
