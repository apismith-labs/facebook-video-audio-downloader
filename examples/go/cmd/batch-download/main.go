// Command batch-download downloads one Actor Run plus one media folder per CSV row.
//
// Without -execute it only validates the file and prints a plan; it reads no token and creates no
// Run.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apismith-labs/facebook-video-audio-downloader/examples/go/downloader"
	"github.com/apismith-labs/facebook-video-audio-downloader/examples/go/internal/cliargs"
	"github.com/apismith-labs/facebook-video-audio-downloader/examples/go/internal/dotenv"
)

type row struct {
	RowID           string
	URL             string
	CanonicalURL    string
	URLForm         string
	DurationSeconds *float64
}

func parseRows(path string) ([]row, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("CSV file not found: %s", path)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("could not parse CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("CSV contains no rows")
	}
	header := map[string]int{}
	for index, name := range records[0] {
		// Excel and Google Sheets prefix the first header with a UTF-8 BOM; the Python, Node, and
		// Java parsers strip it, so a spreadsheet saved from any of them must load here too.
		header[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))] = index
	}
	urlIndex, ok := header["videourl"]
	if !ok {
		urlIndex, ok = header["url"]
	}
	if !ok {
		return nil, fmt.Errorf("CSV must have a 'videoUrl' or 'url' column")
	}
	durationIndex, hasDuration := header["durationseconds"]

	rows := []row{}
	for index := 1; index < len(records); index++ {
		line := strconv.Itoa(index + 1)
		record := records[index]
		if urlIndex >= len(record) {
			continue
		}
		rawURL := strings.TrimSpace(record[urlIndex])
		if rawURL == "" {
			continue
		}
		canonical, form, err := downloader.ValidateVideoURL(rawURL)
		if err != nil {
			return nil, fmt.Errorf("CSV line %s: %w", line, err)
		}
		current := row{RowID: line, URL: rawURL, CanonicalURL: canonical, URLForm: form}
		if hasDuration && durationIndex < len(record) {
			value := strings.TrimSpace(record[durationIndex])
			if value != "" {
				seconds, convErr := strconv.ParseFloat(value, 64)
				if convErr != nil {
					return nil, fmt.Errorf("CSV line %s: durationSeconds must be a number", line)
				}
				if seconds < 0 {
					return nil, fmt.Errorf("CSV line %s: durationSeconds must not be negative", line)
				}
				current.DurationSeconds = &seconds
			}
		}
		rows = append(rows, current)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("CSV contains no non-empty URL rows")
	}
	return rows, nil
}

func estimate(rows []row, tier string) (map[string]any, error) {
	total := 0.0
	minutes := 0
	for _, current := range rows {
		seconds := 0.0
		if current.DurationSeconds != nil {
			seconds = *current.DurationSeconds
		}
		cost, err := downloader.EstimateCost(1, seconds, tier)
		if err != nil {
			return nil, err
		}
		total += cost
		minutes += downloader.BilledMinutes(seconds)
	}
	return map[string]any{
		"tier":          strings.ToUpper(tier),
		"videos":        len(rows),
		"billedMinutes": minutes,
		"totalUsd":      total,
		"lowerBound":    true,
	}, nil
}

func main() {
	execute := flag.Bool("execute", false, "actually create Runs and download")
	out := flag.String("out", "downloads", "output base directory")
	limit := flag.Int("limit", 0, "process only the first N rows")
	concurrency := flag.Int("max-concurrency", 3, "concurrent rows")
	output := flag.String("output", "", "result JSON path")
	tier := flag.String("tier", "FREE", "Apify plan tier for the estimate")
	noVideo := flag.Bool("no-video", false, "skip video downloads")
	audio := flag.Bool("audio", false, "also download audio")
	audioFormat := flag.String("audio-format", "raw", "raw, m4a, or mp3")
	force := flag.Bool("force", false, "re-download existing files")
	timeout := flag.Duration("timeout", 900*time.Second, "per-Run deadline")
	build := flag.String("build", downloader.ApprovedBuildNumber, "Actor Build")
	actorID := flag.String("actor-id", "", "override the Actor ID")
	datasetLimit := flag.Int("dataset-limit", 100, "maximum Dataset items per Run")
	chargeCap := flag.String("max-total-charge-usd", "", "per-Run charge cap in USD")
	requireCap := flag.Bool("require-charge-cap", false, "fail before any Run without a cap")
	// The documented form puts the CSV path first, so reorder before the flag package stops at it.
	flag.CommandLine.Parse(cliargs.Reorder(os.Args[1:], map[string]bool{
		"out": true, "limit": true, "max-concurrency": true, "output": true, "tier": true,
		"audio-format": true, "timeout": true, "build": true, "actor-id": true,
		"dataset-limit": true, "max-total-charge-usd": true,
	}))

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: batch-download <file.csv> [-execute]")
		os.Exit(2)
	}
	csvPath, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Configuration error:", err)
		os.Exit(2)
	}

	rows, err := parseRows(csvPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Configuration error:", err)
		os.Exit(2)
	}
	if *limit > 0 && *limit < len(rows) {
		rows = rows[:*limit]
	}
	estimateValue, err := estimate(rows, *tier)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Configuration error:", err)
		os.Exit(2)
	}

	fmt.Printf("CSV: %s\nRows: %d\n", csvPath, len(rows))
	fmt.Printf("Estimate: %d video(s), %v billed minute(s), %s tier -> %s estimated (media transfer is free)\n",
		estimateValue["videos"], estimateValue["billedMinutes"], estimateValue["tier"],
		downloader.FormatUSD(estimateValue["totalUsd"].(float64)))
	absoluteOut, _ := filepath.Abs(*out)
	fmt.Printf("Output: %s\n", absoluteOut)

	if !*execute {
		fmt.Println("Plan only. No Run was created and no token was read.")
		fmt.Println("Re-run with -execute to download these rows.")
		os.Exit(0)
	}

	dotenv.Load()
	if strings.TrimSpace(os.Getenv("APIFY_TOKEN")) == "" {
		fmt.Fprintln(os.Stderr, "Configuration error: APIFY_TOKEN is required when -execute is used")
		os.Exit(2)
	}

	results := make([]downloader.Report, len(rows))
	codes := make([]int, len(rows))
	workers := *concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(rows) {
		workers = len(rows)
	}
	var wg sync.WaitGroup
	next := make(chan int)
	go func() {
		for index := range rows {
			next <- index
		}
		close(next)
	}()
	var printMu sync.Mutex
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range next {
				report, code := downloader.DownloadOne(context.Background(), rows[index].URL, downloader.DownloadOptions{
					OutDir:      *out,
					WantVideo:   !*noVideo,
					WantAudio:   *audio,
					AudioFormat: *audioFormat,
					Force:       *force,
					Run: downloader.Options{
						ActorID:           *actorID,
						Build:             *build,
						Timeout:           *timeout,
						MaxTotalChargeUSD: *chargeCap,
						RequireChargeCap:  *requireCap,
						DatasetLimit:      *datasetLimit,
						RowID:             rows[index].RowID,
					},
				})
				results[index] = report
				codes[index] = code
				printMu.Lock()
				fmt.Printf("row %s: %s\n", rows[index].RowID, report.Status)
				printMu.Unlock()
			}
		}()
	}
	wg.Wait()

	counts := map[string]int{}
	failed := []string{}
	for index, report := range results {
		counts[report.Status]++
		if codes[index] != 0 {
			failed = append(failed, rows[index].RowID)
		}
	}
	payload := map[string]any{
		"generatedAt": time.Now().UTC().Format(time.RFC3339),
		"actorId":     *actorID,
		"build":       *build,
		"source":      csvPath,
		"outputDir":   absoluteOut,
		"estimate":    estimateValue,
		"counts":      counts,
		"results":     results,
	}
	resultPath := *output
	if resultPath == "" {
		resultPath = filepath.Join(filepath.Dir(csvPath),
			"facebook-batch-download-"+time.Now().UTC().Format("20060102T150405Z")+".json")
	}
	encoded, _ := json.MarshalIndent(payload, "", "  ")
	if err := os.WriteFile(resultPath, append(encoded, '\n'), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "could not write result file:", err)
		os.Exit(1)
	}
	fmt.Printf("Result: %s\n", resultPath)
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "Rows that did not finish cleanly: %s\n", strings.Join(failed, ", "))
		os.Exit(1)
	}
}
