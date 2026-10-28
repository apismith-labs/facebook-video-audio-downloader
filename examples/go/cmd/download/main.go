// Command download fetches the video, audio, transcript, and metadata for one Facebook video URL.
//
// One invocation may create one billable Actor Run ($0.0055 for a video under one minute). The Run
// resolves the page into signed videoUrl/audioUrl links; those are downloaded immediately because
// the signature expires.
//
// Exit codes: 0 downloaded, 2 bad configuration, 4 Actor item error, 5 empty Dataset, 7 unknown,
// 8 transport failure, 9 Run failed, 10 creation unknown, 11 media download failed.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apismith-labs/facebook-video-audio-downloader/examples/go/downloader"
	"github.com/apismith-labs/facebook-video-audio-downloader/examples/go/internal/cliargs"
	"github.com/apismith-labs/facebook-video-audio-downloader/examples/go/internal/dotenv"
)

func humanBytes(value int64) string {
	if value <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", value)
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}

func makeProgress(quiet bool) func(string, int64, int64) {
	state := map[string]int64{}
	return func(kind string, written, total int64) {
		if quiet {
			return
		}
		if written-state[kind] < 5*1024*1024 && !(total > 0 && written >= total) {
			return
		}
		state[kind] = written
		share := ""
		if total > 0 {
			share = fmt.Sprintf(" (%d%%)", written*100/total)
		}
		fmt.Fprintf(os.Stderr, "  %s: %s%s\r", kind, humanBytes(written), share)
	}
}

func main() {
	out := flag.String("out", "downloads", "output base directory")
	name := flag.String("name", "", "override the output folder name")
	noVideo := flag.Bool("no-video", false, "do not download video.mp4")
	audio := flag.Bool("audio", false, "also download the audio track")
	audioFormat := flag.String("audio-format", "raw", "raw, m4a, or mp3")
	force := flag.Bool("force", false, "re-download existing files")
	keepMediaURLs := flag.Bool("keep-media-urls", false, "keep signed URLs in metadata.json")
	asJSON := flag.Bool("json", false, "print a JSON report")
	reportPath := flag.String("report", "", "also write the JSON report to this path")
	resume := flag.String("resume", "", "reuse an existing Run (GET only)")
	datasetLimit := flag.Int("dataset-limit", 100, "maximum Dataset items to read")
	timeout := flag.Duration("timeout", 900*time.Second, "overall Run deadline")
	build := flag.String("build", downloader.ApprovedBuildNumber, "Actor Build")
	actorID := flag.String("actor-id", "", "override the Actor ID")
	chargeCap := flag.String("max-total-charge-usd", "", "per-Run charge cap in USD")
	requireCap := flag.Bool("require-charge-cap", false, "fail before creating a Run without a cap")
	quiet := flag.Bool("quiet", false, "suppress progress output")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: download <facebook-video-url> [options]\n\n")
		flag.PrintDefaults()
	}
	// The documented form puts the URL first, so reorder before the flag package stops at it.
	flag.CommandLine.Parse(cliargs.Reorder(os.Args[1:], map[string]bool{
		"out": true, "name": true, "audio-format": true, "report": true, "resume": true,
		"dataset-limit": true, "timeout": true, "build": true, "actor-id": true,
		"max-total-charge-usd": true,
	}))

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if *audioFormat != "raw" && !*audio {
		fmt.Fprintln(os.Stderr, "--audio-format requires --audio")
		os.Exit(2)
	}
	if *audioFormat != "raw" && *audioFormat != "m4a" && *audioFormat != "mp3" {
		fmt.Fprintln(os.Stderr, "--audio-format must be raw, m4a, or mp3")
		os.Exit(2)
	}

	dotenv.Load()

	report, code := downloader.DownloadOne(context.Background(), flag.Arg(0), downloader.DownloadOptions{
		OutDir:        *out,
		WantVideo:     !*noVideo,
		WantAudio:     *audio,
		AudioFormat:   *audioFormat,
		Force:         *force,
		KeepMediaURLs: *keepMediaURLs,
		Name:          *name,
		Progress:      makeProgress(*quiet),
		Run: downloader.Options{
			ActorID:           *actorID,
			Build:             *build,
			Timeout:           *timeout,
			MaxTotalChargeUSD: *chargeCap,
			RequireChargeCap:  *requireCap,
			DatasetLimit:      *datasetLimit,
			RunID:             *resume,
		},
	})

	if *asJSON {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not encode report:", err)
			os.Exit(1)
		}
		fmt.Println(string(encoded))
	} else if !*quiet {
		if code == 0 {
			names := []string{}
			for _, asset := range report.Assets {
				names = append(names, filepath.Base(asset.Path))
			}
			if len(names) == 0 {
				names = []string{"no media"}
			}
			fmt.Printf("%s: %s  segments=%d  %s\n", report.Status, *report.OutputDir, report.Segments, strings.Join(names, ", "))
		} else {
			detail := "no output"
			if report.Error != nil {
				detail = *report.Error
			}
			fmt.Fprintf(os.Stderr, "%s: %s\n", report.Status, detail)
		}
	}

	if *reportPath != "" {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			_ = os.MkdirAll(filepath.Dir(*reportPath), 0o755)
			_ = os.WriteFile(*reportPath, append(encoded, '\n'), 0o600)
		}
	}
	os.Exit(code)
}
