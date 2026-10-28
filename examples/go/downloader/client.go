package downloader

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ConfigurationError covers an invalid URL, credentials, timeout, Actor ID,
// Build, or charge-cap setting: everything detected before a request is sent.
type ConfigurationError struct{ Message string }

func (e *ConfigurationError) Error() string { return e.Message }

// ProtocolError means the runner returned a malformed or contradictory result.
type ProtocolError struct {
	Message   string
	RunID     string
	DatasetID string
}

func (e *ProtocolError) Error() string { return e.Message }

// CreationUnknownError means the create POST may have produced a Run.
type CreationUnknownError struct{ Message string }

func (e *CreationUnknownError) Error() string { return e.Message }

// TransportError is a definite transport or post-creation retrieval failure.
type TransportError struct {
	Message   string
	RunID     string
	DatasetID string
}

func (e *TransportError) Error() string { return e.Message }

// Result is the outcome of one Actor Run. Items keeps the Actor's original
// fields, including ones this client does not know about.
type Result struct {
	Status          string
	Text            string
	HasText         bool
	Segments        []map[string]any
	Items           []any
	RunID           string
	DatasetID       string
	SourceURL       string
	CanonicalURL    string
	URLForm         string
	Error           string
	DatasetComplete bool
	Title           string
	Nickname        string
	Duration        float64
	HasDuration     bool
}

// Succeeded is true only for the success status.
func (r Result) Succeeded() bool { return r.Status == "success" }

// FirstItem returns the first Dataset item, or nil.
func (r Result) FirstItem() map[string]any {
	if len(r.Items) == 0 {
		return nil
	}
	item, _ := r.Items[0].(map[string]any)
	return item
}

// ExitCode maps a business status to the documented process exit code.
func (r Result) ExitCode() int {
	if code, ok := ExitCodes[r.Status]; ok {
		return code
	}
	return ExitProtocol
}

// Options configures GetTranscript. Zero values fall back to environment
// variables and the same defaults as the Python client.
type Options struct {
	Token             string
	ActorID           string
	Build             string
	Timeout           time.Duration
	MaxTotalChargeUSD string
	RequireChargeCap  bool
	DatasetLimit      int
	RunID             string
	RowID             string
	Transport         Transport
	Journal           Journal
	// Sleep and MaxAttempts are test seams; production callers leave them zero.
	Sleep       func(time.Duration)
	MaxAttempts int
}

// GetTranscript runs one public Facebook video URL through the pinned Build.
//
// Token selection: Options.Token, then APIFY_TOKEN. Transport and Journal are
// injection seams for offline tests. Setting RunID performs a GET-only resume;
// no create POST is issued.
func GetTranscript(ctx context.Context, rawURL string, options Options) (Result, error) {
	canonical, form, err := ValidateVideoURL(rawURL)
	if err != nil {
		return Result{}, &ConfigurationError{Message: err.Error()}
	}

	token := strings.TrimSpace(options.Token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("APIFY_TOKEN"))
	}
	if token == "" {
		return Result{}, &ConfigurationError{Message: "APIFY_TOKEN is required"}
	}

	actorID := options.ActorID
	if strings.TrimSpace(actorID) == "" {
		actorID = DefaultActorID
	}
	actorID, err = ValidateActorID(actorID)
	if err != nil {
		return Result{}, &ConfigurationError{Message: err.Error()}
	}

	build := options.Build
	if strings.TrimSpace(build) == "" {
		build = ApprovedBuildNumber
	}
	build, err = ValidateBuild(build, true)
	if err != nil {
		return Result{}, &ConfigurationError{Message: err.Error()}
	}

	capValue := options.MaxTotalChargeUSD
	if strings.TrimSpace(capValue) == "" {
		capValue = os.Getenv("APIFY_MAX_TOTAL_CHARGE_USD")
	}
	requireCap := options.RequireChargeCap || os.Getenv("APIFY_REQUIRE_MAX_TOTAL_CHARGE_USD") == "1"
	if _, err := ValidateChargeCap(capValue, requireCap); err != nil {
		return Result{}, &ConfigurationError{Message: err.Error()}
	}

	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout * time.Second
	}

	datasetLimit := options.DatasetLimit
	if datasetLimit <= 0 {
		datasetLimit = DefaultDatasetLimit
	}

	activeTransport := options.Transport
	if activeTransport == nil {
		activeTransport = &HTTPTransport{Token: token}
	}

	outcome := ExecuteInput(ctx, RunOptions{
		Transport:         activeTransport,
		ActorID:           actorID,
		Input:             map[string]any{"videoUrl": canonical},
		SourceURL:         canonical,
		Journal:           options.Journal,
		Timeout:           timeout,
		MaxAttempts:       options.MaxAttempts,
		Sleep:             options.Sleep,
		RunID:             options.RunID,
		RowID:             options.RowID,
		MaxTotalChargeUSD: mustChargeCap(capValue),
		RequireChargeCap:  requireCap,
		Build:             build,
		RequireBuild:      true,
		DatasetLimit:      &datasetLimit,
	})

	result := Result{
		Status:       outcome.Status,
		Items:        outcome.RawItems,
		SourceURL:    rawURL,
		CanonicalURL: canonical,
		URLForm:      form,
	}
	if outcome.RunID != nil {
		result.RunID = *outcome.RunID
	}
	if outcome.DatasetID != nil {
		result.DatasetID = *outcome.DatasetID
	}

	switch outcome.Status {
	case "creation_unknown":
		return Result{}, &CreationUnknownError{Message: fmt.Sprintf(
			"Run creation outcome is unknown (Run ID unavailable): %s; do not call GetTranscript again blindly",
			firstNonEmpty(outcome.Error, "reconcile before retrying"))}
	case "transport_error":
		if outcome.Stage == "build_mismatch" {
			return Result{}, &ProtocolError{
				Message: fmt.Sprintf("Run was created but its Build could not be verified: %s; do not create a replacement Run automatically",
					firstNonEmpty(outcome.Error, "Build mismatch")),
				RunID:     result.RunID,
				DatasetID: result.DatasetID,
			}
		}
		detail := firstNonEmpty(outcome.Error, "Apify transport failed")
		if result.RunID != "" {
			detail = fmt.Sprintf("%s; Run %s already exists. Reconcile or resume that Run; do not call GetTranscript again to replace it",
				detail, result.RunID)
		}
		return Result{}, &TransportError{Message: detail, RunID: result.RunID, DatasetID: result.DatasetID}
	case "run_failed":
		result.Error = firstNonEmpty(outcome.Error, "Actor Run failed")
		return result, nil
	}

	if _, ok := ExitCodes[outcome.Status]; !ok {
		return Result{}, &ProtocolError{
			Message:   fmt.Sprintf("Runner returned unsupported status: %q", outcome.Status),
			RunID:     result.RunID,
			DatasetID: result.DatasetID,
		}
	}

	var transcriptItem map[string]any
	for _, item := range result.Items {
		object, ok := item.(map[string]any)
		if !ok {
			return Result{}, &ProtocolError{Message: "Runner returned malformed Dataset items", RunID: result.RunID, DatasetID: result.DatasetID}
		}
		if transcriptItem == nil {
			if text, ok := object["text"].(string); ok && strings.TrimSpace(text) != "" {
				transcriptItem = object
			}
		}
	}

	if transcriptItem != nil {
		result.Text, _ = transcriptItem["text"].(string)
		result.HasText = strings.TrimSpace(result.Text) != ""
		result.Segments = NormalizeSegments(transcriptItem["segments"])
	}
	if result.Status == "success" && !result.HasText {
		return Result{}, &ProtocolError{
			Message:   "Runner reported success without a non-empty transcript",
			RunID:     result.RunID,
			DatasetID: result.DatasetID,
		}
	}

	if outcome.DatasetComplete != nil {
		result.DatasetComplete = *outcome.DatasetComplete
	}
	metadata := transcriptItem
	if metadata == nil && len(result.Items) > 0 {
		metadata, _ = result.Items[0].(map[string]any)
	}
	if metadata != nil {
		result.Title = stringValue(metadata["title"])
		result.Nickname = stringValue(metadata["nickname"])
		if duration, ok := number(metadata["duration"]); ok {
			result.Duration = duration
			result.HasDuration = true
		}
	}
	result.Error = outcome.Error
	return result, nil
}

func mustChargeCap(value string) *float64 {
	parsed, err := ValidateChargeCap(value, false)
	if err != nil {
		return nil
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// EstimateCost implements the two-event pricing model:
// $0.001 per dataset item plus a per-started-minute transcription fee.
func EstimateCost(videos int, secondsPerVideo float64, tier string) (float64, error) {
	price, ok := TierPricesUSD[tier]
	if !ok {
		return 0, fmt.Errorf("unknown tier %q", tier)
	}
	if videos < 0 {
		return 0, fmt.Errorf("videos must not be negative")
	}
	if secondsPerVideo < 0 {
		return 0, fmt.Errorf("duration must not be negative")
	}
	minutes := BilledMinutes(secondsPerVideo)
	return float64(videos) * (DatasetItemUSD + float64(minutes)*price), nil
}

// FormatUSD renders an estimate the same way the other clients do.
func FormatUSD(amount float64) string {
	return "$" + strconv.FormatFloat(amount, 'f', 4, 64)
}
