package downloader

// Media download layer. A signed fbcdn link is short-lived: download immediately after the Run
// succeeds, resume with HTTP Range only within the same link, and rename into place only after the
// byte count and SHA-256 have been verified.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	MediaChunkSize          = 1 << 20
	DefaultMediaTimeout     = 60 * time.Second
	DefaultMediaMaxAttempts = 3
	DownloadFailedExitCode  = 11
	ManifestSchemaVersion   = 1
	mediaTitleLimit         = 48
)

var retryableStatuses = map[int]bool{408: true, 425: true, 429: true, 500: true, 502: true, 503: true, 504: true}
var contentRangeTotal = regexp.MustCompile(`/(\d+)\s*$`)
var signedQueryKeys = map[string]bool{
	"oh": true, "oe": true, "sig": true, "signature": true, "token": true, "hdnea": true,
	"hash": true, "expires": true, "expire": true, "x-amz-signature": true,
	"x-amz-credential": true, "x-amz-security-token": true,
}

// MediaError is a download failure. Status is the HTTP status when a response was received.
type MediaError struct {
	Message   string
	Status    int
	Retryable bool
}

func (e *MediaError) Error() string { return e.Message }

// MediaAsset describes one downloaded or reused file.
type MediaAsset struct {
	Kind        string `json:"kind"`
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"contentType"`
	SourceHost  string `json:"sourceHost"`
	Skipped     bool   `json:"skipped"`
}

// MediaOptions configures a single download.
type MediaOptions struct {
	Kind        string
	Timeout     time.Duration
	MaxAttempts int
	Force       bool
	Progress    func(kind string, written, total int64)
	Client      *http.Client
	Sleep       func(time.Duration)
}

// RedactMediaURL returns scheme://host/path, dropping the signed query string.
func RedactMediaURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "[unparseable media url]"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// MediaHost returns the lower-case host only.
func MediaHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
}

func sha256Of(path string, chunk int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.CopyBuffer(digest, file, make([]byte, chunk)); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func parseContentRangeTotal(value string) int64 {
	match := contentRangeTotal.FindStringSubmatch(value)
	if match == nil {
		return -1
	}
	total, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return -1
	}
	return total
}

// DownloadMedia downloads rawURL to destination, resuming a .part file when the server supports it.
// An existing destination is returned with Skipped=true unless Force is set.
func DownloadMedia(ctx context.Context, rawURL, destination string, options MediaOptions) (MediaAsset, error) {
	kind := options.Kind
	if kind == "" {
		kind = "video"
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultMediaTimeout
	}
	attempts := options.MaxAttempts
	if attempts <= 0 {
		attempts = DefaultMediaMaxAttempts
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return MediaAsset{}, &MediaError{Message: "media URL must be an absolute http(s) URL"}
	}

	host := MediaHost(rawURL)
	if !options.Force {
		if info, err := os.Stat(destination); err == nil {
			digest, hashErr := sha256Of(destination, MediaChunkSize)
			if hashErr != nil {
				return MediaAsset{}, &MediaError{Message: "could not hash existing file: " + hashErr.Error()}
			}
			return MediaAsset{Kind: kind, Path: destination, Bytes: info.Size(), SHA256: digest,
				SourceHost: host, Skipped: true}, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return MediaAsset{}, &MediaError{Message: "could not create output folder: " + err.Error()}
	}
	partPath := destination + ".part"
	if options.Force {
		_ = os.Remove(partPath)
		_ = os.Remove(destination)
	}

	var lastErr *MediaError
	for attempt := 0; attempt < attempts; attempt++ {
		var resumeFrom int64
		if info, err := os.Stat(partPath); err == nil {
			resumeFrom = info.Size()
		}

		asset, err := mediaAttempt(ctx, client, rawURL, destination, partPath, resumeFrom, kind, host, options.Progress)
		if err == nil {
			return asset, nil
		}

		var mediaErr *MediaError
		if !errors.As(err, &mediaErr) {
			return MediaAsset{}, err
		}
		lastErr = mediaErr
		if !mediaErr.Retryable || attempt+1 >= attempts {
			return MediaAsset{}, mediaErr
		}
		sleep(time.Duration(1<<uint(attempt)) * time.Second)
	}
	if lastErr == nil {
		lastErr = &MediaError{Message: "media download failed"}
	}
	return MediaAsset{}, lastErr
}

func mediaAttempt(ctx context.Context, client *http.Client, rawURL, destination, partPath string,
	resumeFrom int64, kind, host string, progress func(string, int64, int64)) (MediaAsset, error) {

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return MediaAsset{}, &MediaError{Message: "could not build media request: " + err.Error()}
	}
	if resumeFrom > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
	}

	response, err := client.Do(request)
	if err != nil {
		return MediaAsset{}, &MediaError{Message: "media download transport failure: " + err.Error(), Retryable: true}
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable && resumeFrom > 0 {
		total := parseContentRangeTotal(response.Header.Get("Content-Range"))
		if total >= 0 {
			if info, statErr := os.Stat(partPath); statErr == nil && info.Size() == total {
				if renameErr := os.Rename(partPath, destination); renameErr == nil {
					digest, _ := sha256Of(destination, MediaChunkSize)
					return MediaAsset{Kind: kind, Path: destination, Bytes: total, SHA256: digest, SourceHost: host}, nil
				}
			}
		}
	}

	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return MediaAsset{}, &MediaError{
			Message:   fmt.Sprintf("media server returned HTTP %d", response.StatusCode),
			Status:    response.StatusCode,
			Retryable: retryableStatuses[response.StatusCode],
		}
	}

	appendMode := resumeFrom > 0 && response.StatusCode == http.StatusPartialContent
	flags := os.O_WRONLY | os.O_CREATE
	if appendMode {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	stream, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return MediaAsset{}, &MediaError{Message: "could not open partial file: " + err.Error()}
	}

	written := int64(0)
	if appendMode {
		written = resumeFrom
	}
	declared := response.ContentLength
	total := parseContentRangeTotal(response.Header.Get("Content-Range"))
	expected := total
	if expected < 0 {
		if appendMode && declared >= 0 {
			expected = resumeFrom + declared
		} else {
			expected = declared
		}
	}

	buffer := make([]byte, MediaChunkSize)
	for {
		read, readErr := response.Body.Read(buffer)
		if read > 0 {
			if _, writeErr := stream.Write(buffer[:read]); writeErr != nil {
				stream.Close()
				return MediaAsset{}, &MediaError{Message: "could not write media file: " + writeErr.Error()}
			}
			written += int64(read)
			if progress != nil {
				progress(kind, written, expected)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			stream.Close()
			return MediaAsset{}, &MediaError{Message: "media download transport failure: " + readErr.Error(), Retryable: true}
		}
	}
	if err := stream.Close(); err != nil {
		return MediaAsset{}, &MediaError{Message: "could not flush media file: " + err.Error()}
	}

	info, err := os.Stat(partPath)
	if err != nil {
		return MediaAsset{}, &MediaError{Message: "could not stat partial file: " + err.Error()}
	}
	if expected >= 0 && info.Size() != expected {
		return MediaAsset{}, &MediaError{
			Message:   fmt.Sprintf("truncated download: expected %d bytes, wrote %d", expected, info.Size()),
			Retryable: true,
		}
	}
	if err := os.Rename(partPath, destination); err != nil {
		return MediaAsset{}, &MediaError{Message: "could not finalise media file: " + err.Error()}
	}
	digest, err := sha256Of(destination, MediaChunkSize)
	if err != nil {
		return MediaAsset{}, &MediaError{Message: "could not hash media file: " + err.Error()}
	}
	return MediaAsset{
		Kind: kind, Path: destination, Bytes: info.Size(), SHA256: digest,
		ContentType: response.Header.Get("Content-Type"), SourceHost: host,
	}, nil
}

// AudioTarget maps a raw audio.mp4 to the requested container.
func AudioTarget(destination, audioFormat string) (string, bool, error) {
	switch audioFormat {
	case "raw":
		return destination, false, nil
	case "m4a", "mp3":
		base := strings.TrimSuffix(destination, filepath.Ext(destination))
		return base + "." + audioFormat, true, nil
	default:
		return "", false, &MediaError{Message: "audioFormat must be one of: raw, m4a, mp3"}
	}
}

// FfmpegArgs builds the argument list for an audio container change.
func FfmpegArgs(source, target, audioFormat string) ([]string, error) {
	switch audioFormat {
	case "m4a":
		return []string{"ffmpeg", "-nostdin", "-y", "-loglevel", "error", "-i", source,
			"-vn", "-c:a", "copy", "-movflags", "+faststart", target}, nil
	case "mp3":
		return []string{"ffmpeg", "-nostdin", "-y", "-loglevel", "error", "-i", source,
			"-vn", "-c:a", "libmp3lame", "-b:a", "192k", target}, nil
	default:
		return nil, &MediaError{Message: "transcode is only defined for m4a and mp3"}
	}
}

// TranscodeAudio converts an audio-only MP4 into m4a or mp3. Runner is injectable for tests.
func TranscodeAudio(source, target, audioFormat string, runner func(name string, args ...string) error) error {
	if _, err := os.Stat(source); err != nil {
		return &MediaError{Message: "audio source is missing: " + source}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return &MediaError{Message: "could not create output folder: " + err.Error()}
	}
	args, err := FfmpegArgs(source, target, audioFormat)
	if err != nil {
		return err
	}
	if runner != nil {
		if runErr := runner(args[0], args[1:]...); runErr != nil {
			return &MediaError{Message: "ffmpeg failed: " + runErr.Error()}
		}
		return nil
	}
	if _, lookErr := exec.LookPath("ffmpeg"); lookErr != nil {
		return &MediaError{Message: "ffmpeg is required for --audio-format " + audioFormat + "; install it or use raw"}
	}
	command := exec.Command(args[0], args[1:]...)
	var stderr strings.Builder
	command.Stderr = &stderr
	if runErr := command.Run(); runErr != nil {
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		detail := lines[len(lines)-1]
		if len(detail) > 160 {
			detail = detail[:160]
		}
		if detail == "" {
			detail = runErr.Error()
		}
		return &MediaError{Message: "ffmpeg failed: " + detail}
	}
	return nil
}
