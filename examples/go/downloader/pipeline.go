package downloader

// One-video download pipeline: Actor Run, media download, sidecar files, manifest.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Report is the machine-readable outcome of one download, shared by every CLI.
type Report struct {
	Status       string           `json:"status"`
	SourceURL    string           `json:"sourceUrl"`
	CanonicalURL *string          `json:"canonicalUrl"`
	URLForm      *string          `json:"urlForm"`
	RunID        *string          `json:"runId"`
	DatasetID    *string          `json:"datasetId"`
	OutputDir    *string          `json:"outputDir"`
	Duration     *float64         `json:"duration"`
	Title        *string          `json:"title"`
	Nickname     *string          `json:"nickname"`
	Segments     int              `json:"segments"`
	Assets       []MediaAsset     `json:"assets"`
	Transcript   *TranscriptStats `json:"transcript"`
	Error        *string          `json:"error"`
}

// DownloadOptions controls one download. Run carries the Actor Run settings.
type DownloadOptions struct {
	OutDir        string
	WantVideo     bool
	WantAudio     bool
	AudioFormat   string
	Force         bool
	KeepMediaURLs bool
	Name          string
	Run           Options
	Progress      func(kind string, written, total int64)
	MediaClient   *http.Client
	Sleep         func(time.Duration)
}

var downloadableStatuses = map[string]bool{"success": true, "no_transcript": true}
var transcriptExitCodes = map[string]int{"actor_error": 4, "empty": 5, "no_transcript": 6, "unknown": 7, "run_failed": 9}

func pointer[T any](value T) *T { return &value }

// DownloadOne resolves one URL, downloads the requested media, and writes the sidecar files.
func DownloadOne(ctx context.Context, rawURL string, options DownloadOptions) (Report, int) {
	report := Report{Status: "unknown", SourceURL: rawURL, Assets: []MediaAsset{}}
	if options.OutDir == "" {
		options.OutDir = "downloads"
	}
	if options.AudioFormat == "" {
		options.AudioFormat = "raw"
	}

	result, err := GetTranscript(ctx, rawURL, options.Run)
	if err != nil {
		var configErr *ConfigurationError
		if errors.As(err, &configErr) {
			report.Status = "config_error"
			report.Error = pointer(configErr.Message)
			return report, 2
		}
		var creationErr *CreationUnknownError
		if errors.As(err, &creationErr) {
			report.Status = "creation_unknown"
			report.Error = pointer(creationErr.Message)
			return report, 10
		}
		var protocolErr *ProtocolError
		if errors.As(err, &protocolErr) {
			report.Status = "unknown"
			if protocolErr.RunID != "" {
				report.RunID = pointer(protocolErr.RunID)
			}
			if protocolErr.DatasetID != "" {
				report.DatasetID = pointer(protocolErr.DatasetID)
			}
			report.Error = pointer(protocolErr.Message)
			return report, 7
		}
		var transportErr *TransportError
		if errors.As(err, &transportErr) {
			report.Status = "transport_error"
			if transportErr.RunID != "" {
				report.RunID = pointer(transportErr.RunID)
			}
			if transportErr.DatasetID != "" {
				report.DatasetID = pointer(transportErr.DatasetID)
			}
			report.Error = pointer(transportErr.Message)
			return report, 8
		}
		report.Error = pointer(err.Error())
		return report, 7
	}

	report.Status = result.Status
	report.CanonicalURL = pointer(result.CanonicalURL)
	report.URLForm = pointer(result.URLForm)
	if result.RunID != "" {
		report.RunID = pointer(result.RunID)
	}
	if result.DatasetID != "" {
		report.DatasetID = pointer(result.DatasetID)
	}
	if result.HasDuration {
		report.Duration = pointer(result.Duration)
	}
	if result.Title != "" {
		report.Title = pointer(result.Title)
	}
	if result.Nickname != "" {
		report.Nickname = pointer(result.Nickname)
	}
	if result.Error != "" {
		report.Error = pointer(result.Error)
	}

	item := result.FirstItem()
	folder := options.Name
	if folder == "" {
		folder = FolderName(result.CanonicalURL, result.URLForm, result.Title)
	}
	target := ChooseOutputDir(options.OutDir, folder, result.CanonicalURL)
	report.OutputDir = pointer(target)

	if !downloadableStatuses[result.Status] {
		if _, writeErr := WriteJSON(filepath.Join(target, metadataFile),
			SanitizeItem(item, options.KeepMediaURLs)); writeErr != nil {
			report.Error = pointer("could not write metadata: " + writeErr.Error())
		}
		code, ok := transcriptExitCodes[result.Status]
		if !ok {
			code = 7
		}
		return report, code
	}

	segments := TranscriptSegments(result.Segments)
	transcriptText := ""
	if result.HasText {
		transcriptText = result.Text
	}
	if transcriptText != "" {
		transcriptText += "\n"
	}
	transcriptBytes, err := WriteText(filepath.Join(target, transcriptFile), transcriptText)
	if err != nil {
		report.Error = pointer("could not write transcript: " + err.Error())
		return report, DownloadFailedExitCode
	}
	if _, err := WriteJSON(filepath.Join(target, segmentsFile), segments); err != nil {
		report.Error = pointer("could not write segments: " + err.Error())
		return report, DownloadFailedExitCode
	}
	if _, err := WriteJSON(filepath.Join(target, metadataFile), SanitizeItem(item, options.KeepMediaURLs)); err != nil {
		report.Error = pointer("could not write metadata: " + err.Error())
		return report, DownloadFailedExitCode
	}
	report.Segments = len(segments)
	report.Transcript = &TranscriptStats{Bytes: transcriptBytes, Segments: len(segments)}

	type job struct {
		kind        string
		url         string
		destination string
		converted   string
	}
	jobs := []job{}
	if options.WantVideo {
		if videoURL, ok := item["videoUrl"].(string); ok && videoURL != "" {
			jobs = append(jobs, job{kind: "video", url: videoURL, destination: filepath.Join(target, "video.mp4")})
		}
	}
	if options.WantAudio {
		if audioURL, ok := item["audioUrl"].(string); ok && audioURL != "" {
			rawPath := filepath.Join(target, "audio.mp4")
			finalPath, remux, targetErr := AudioTarget(rawPath, options.AudioFormat)
			if targetErr != nil {
				report.Error = pointer(targetErr.Error())
				return report, 2
			}
			current := job{kind: "audio", url: audioURL, destination: rawPath}
			if remux {
				current.converted = finalPath
			}
			jobs = append(jobs, current)
		}
	}

	var failure error
	if len(jobs) == 0 {
		missing := &MediaError{Message: fmt.Sprintf(
			"the Dataset item carries no media URL for the requested output (%s)", result.Status)}
		failure = missing
		report.Error = pointer(missing.Error())
	}

	assets := []MediaAsset{}
	sources := []MediaSource{}
	for _, current := range jobs {
		sources = append(sources, MediaSource{Kind: current.kind, URL: RedactMediaURL(current.url)})
		asset, downloadErr := DownloadMedia(ctx, current.url, current.destination, MediaOptions{
			Kind:     current.kind,
			Force:    options.Force,
			Progress: options.Progress,
			Client:   options.MediaClient,
			Sleep:    options.Sleep,
		})
		if downloadErr != nil {
			failure = downloadErr
			report.Error = pointer(current.kind + " download failed: " + downloadErr.Error())
			break
		}
		assets = append(assets, asset)
		if current.converted != "" {
			_, statErr := os.Stat(current.converted)
			// --force re-derives the converted container too; otherwise a re-downloaded audio.mp4
			// would sit next to a stale audio.m4a.
			rebuilt := statErr != nil || options.Force
			if rebuilt {
				if transcodeErr := TranscodeAudio(current.destination, current.converted, options.AudioFormat, nil); transcodeErr != nil {
					failure = transcodeErr
					report.Error = pointer("audio conversion failed: " + transcodeErr.Error())
					break
				}
			}
			if info, statErr := os.Stat(current.converted); statErr == nil {
				assets = append(assets, MediaAsset{
					Kind:    "audio-converted",
					Path:    current.converted,
					Bytes:   info.Size(),
					Skipped: !rebuilt,
				})
			}
		}
	}

	manifest := NewManifest(result, assets, transcriptBytes, len(segments), sources)
	if _, err := WriteJSON(filepath.Join(target, manifestFile), manifest); err != nil {
		report.Error = pointer("could not write manifest: " + err.Error())
		return report, DownloadFailedExitCode
	}
	report.Assets = assets
	if failure != nil {
		return report, DownloadFailedExitCode
	}
	return report, 0
}
