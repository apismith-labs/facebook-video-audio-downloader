package downloader

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ApiError carries the HTTP status and whether the request may have had an
// effect upstream. A POST is ambiguous by nature: the Run may exist.
type ApiError struct {
	Message    string
	Status     int // 0 when the failure happened before a response arrived
	RetryAfter time.Duration
	Ambiguous  bool
}

func (e *ApiError) Error() string { return e.Message }

func (e *ApiError) retryable() bool {
	return e.Status == 0 || e.Status == 429 || (e.Status >= 500 && e.Status < 600)
}

// Transport is the injection seam used by the offline tests. It returns the
// decoded JSON value as-is, because a Dataset response is a JSON array while a
// Run response is an object.
type Transport interface {
	Request(ctx context.Context, method, path string, body any, query url.Values) (any, error)
}

// HTTPTransport performs exactly one request per call. Retries belong to the
// staged runner, because create, poll and read have different rules.
type HTTPTransport struct {
	Token   string
	BaseURL string
	Client  *http.Client
}

func (t *HTTPTransport) Request(ctx context.Context, method, path string, body any, query url.Values) (any, error) {
	base := strings.TrimRight(t.BaseURL, "/")
	if base == "" {
		base = "https://api.apify.com/v2"
	}
	target := base + path
	if len(query) > 0 {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		target += separator + query.Encode()
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, &ApiError{Message: "Request body could not be encoded"}
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), target, payload)
	if err != nil {
		return nil, &ApiError{Message: "Request could not be built", Ambiguous: strings.EqualFold(method, "POST")}
	}
	request.Header.Set("Authorization", "Bearer "+t.Token)
	request.Header.Set("Content-Type", "application/json")

	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, &ApiError{
			Message:   fmt.Sprintf("Apify %s transport failure: %T", strings.ToUpper(method), err),
			Ambiguous: strings.EqualFold(method, "POST"),
		}
	}
	defer response.Body.Close()

	raw, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return nil, &ApiError{
			Message:   "Apify response could not be read",
			Status:    response.StatusCode,
			Ambiguous: strings.EqualFold(method, "POST"),
		}
	}

	if response.StatusCode >= 400 {
		return nil, &ApiError{
			Message:    fmt.Sprintf("Apify HTTP %d", response.StatusCode),
			Status:     response.StatusCode,
			RetryAfter: parseRetryAfter(response.Header.Get("Retry-After")),
			Ambiguous:  strings.EqualFold(method, "POST") && response.StatusCode >= 500,
		}
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, &ApiError{
			Message:   "Apify returned invalid JSON",
			Status:    response.StatusCode,
			Ambiguous: strings.EqualFold(method, "POST"),
		}
	}
	return decoded, nil
}

func parseRetryAfter(value string) time.Duration {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(trimmed, 64); err == nil {
		if math.IsInf(seconds, 0) || math.IsNaN(seconds) || seconds < 0 {
			return 0
		}
		return time.Duration(seconds * float64(time.Second))
	}
	if when, err := http.ParseTime(trimmed); err == nil {
		delta := time.Until(when)
		if delta < 0 {
			return 0
		}
		return delta
	}
	return 0
}

var (
	reBearerValue = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/-]+=*`)
	reSecretValue = regexp.MustCompile(`(?i)(sessionid|token|authorization|cookie|password|api[_-]?key)(\s*[:=]\s*["']?)[^\s,;&"']+`)
)

func redactText(value string) string {
	value = reBearerValue.ReplaceAllString(value, "${1}[REDACTED]")
	return reSecretValue.ReplaceAllString(value, "${1}${2}[REDACTED]")
}

// SafeError never persists headers, tokens, or arbitrary response bodies.
func SafeError(err error) string {
	if err == nil {
		return ""
	}
	clean := redactText(strings.ReplaceAll(err.Error(), "\n", " "))
	if len(clean) > 300 {
		clean = clean[:300]
	}
	return clean
}

// Journal is append-only by contract: one JSON line per stage transition.
type Journal interface {
	Append(record map[string]any) error
}

// MemoryJournal is the default for the reusable API; it creates no files.
type MemoryJournal struct {
	Records []map[string]any
}

func (j *MemoryJournal) Append(record map[string]any) error {
	j.Records = append(j.Records, copyRecord(record))
	return nil
}

// FileJournal writes JSONL with owner-only permissions where the platform
// supports it, and refuses to continue over a corrupt file.
type FileJournal struct {
	Path string
	mu   sync.Mutex
}

func (j *FileJournal) Append(record map[string]any) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if err := j.validateLocked(); err != nil {
		return err
	}
	if dir := filepath.Dir(j.Path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(j.Path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (j *FileJournal) validateLocked() error {
	file, err := os.Open(j.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(text), &record); err != nil {
			return fmt.Errorf("Run journal is corrupt at line %d; refusing to continue", line)
		}
	}
	return scanner.Err()
}

// Latest returns the last journal record for a Run ID, or nil.
func (j *FileJournal) Latest(runID string) (map[string]any, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	file, err := os.Open(j.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var found map[string]any
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(text), &record); err != nil {
			return nil, fmt.Errorf("Run journal is corrupt at line %d; refusing resume", line)
		}
		if value, ok := record["runId"].(string); ok && value == runID {
			found = record
		}
	}
	return found, scanner.Err()
}

// LoadResumeRecord validates a journal record before a GET-only resume.
func LoadResumeRecord(journal *FileJournal, runID string, actorID string) (map[string]any, error) {
	record, err := journal.Latest(runID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("No journal record found for Run ID %s; refusing resume", runID)
	}
	if value, _ := record["runId"].(string); value != runID {
		return nil, fmt.Errorf("Run journal Actor/Run ID does not match the requested resume; refusing resume")
	}
	if value, _ := record["actorId"].(string); value != actorID {
		return nil, fmt.Errorf("Run journal Actor/Run ID does not match the requested resume; refusing resume")
	}
	if source, _ := record["sourceUrl"].(string); strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("Run journal is missing the source URL; refusing resume")
	}
	return record, nil
}

// ClassifyItems applies the documented precedence:
// empty Dataset, item error, usable text, missing text, unsupported type.
func ClassifyItems(items []any) (string, string) {
	if items == nil {
		return "unknown", "Dataset response is not an item list"
	}
	if len(items) == 0 {
		return "empty", "Dataset contains no items"
	}

	objects := make([]map[string]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return "unknown", "Dataset contains a non-object item"
		}
		objects = append(objects, object)
	}

	var messages []string
	for _, item := range objects {
		if raw, present := item["errMsg"]; present && raw != nil {
			if text := strings.TrimSpace(fmt.Sprint(raw)); text != "" {
				messages = append(messages, text)
			}
		}
	}
	if len(messages) > 0 {
		joined := redactText(strings.Join(messages, "; "))
		if len(joined) > 500 {
			joined = joined[:500]
		}
		return "actor_error", joined
	}

	for _, item := range objects {
		if text, ok := item["text"].(string); ok && strings.TrimSpace(text) != "" {
			return "success", ""
		}
	}
	for _, item := range objects {
		if raw, present := item["text"]; present && raw == nil {
			return "no_transcript", "Transcript text is null"
		}
	}
	for _, item := range objects {
		if _, present := item["text"]; !present {
			return "no_transcript", "Transcript text is absent"
		}
	}
	for _, item := range objects {
		if text, ok := item["text"].(string); ok && strings.TrimSpace(text) == "" {
			return "no_transcript", "Transcript text is empty"
		}
	}
	return "unknown", "Transcript text has an unsupported type"
}

// NormalizeSegments keeps only well-formed entries: finite numeric bounds with
// start >= 0 and end >= start, plus a string text. Raw items stay untouched.
func NormalizeSegments(value any) []map[string]any {
	list, ok := value.([]any)
	if !ok {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(list))
	for _, entry := range list {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		start, startOK := number(object["start"])
		end, endOK := number(object["end"])
		text, textOK := object["text"].(string)
		if !startOK || !endOK || !textOK {
			continue
		}
		if math.IsInf(start, 0) || math.IsNaN(start) || math.IsInf(end, 0) || math.IsNaN(end) {
			continue
		}
		if start < 0 || end < start {
			continue
		}
		result = append(result, map[string]any{"start": start, "end": end, "text": text})
	}
	return result
}

func number(value any) (float64, bool) {
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

// Outcome is the normalized result of one staged execution.
type Outcome struct {
	RowID             string
	SourceURL         string
	RunID             *string
	DatasetID         *string
	Status            string
	Error             string
	Stage             string
	RawItems          []any
	DatasetComplete   *bool
	BuildNumber       string
	BuildID           string
	BuildVerification string
}

// RunOptions are everything ExecuteInput needs. Zero values fall back to the
// same defaults as the Python client.
type RunOptions struct {
	Transport         Transport
	ActorID           string
	Input             map[string]any
	SourceURL         string
	Journal           Journal
	Timeout           time.Duration
	MaxAttempts       int
	Sleep             func(time.Duration)
	RunID             string
	DatasetID         string
	RowID             string
	MaxTotalChargeUSD *float64
	RequireChargeCap  bool
	Build             string
	RequireBuild      bool
	DatasetLimit      *int
}

// ExecuteInput creates at most one Run, polls it, and reads its Dataset.
// Passing RunID switches to a GET-only resume: no POST is issued.
func ExecuteInput(ctx context.Context, options RunOptions) Outcome {
	if options.Timeout <= 0 {
		options.Timeout = DefaultTimeout * time.Second
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = 4
	}
	if options.Sleep == nil {
		options.Sleep = time.Sleep
	}
	if options.Journal == nil {
		options.Journal = &MemoryJournal{}
	}
	if options.ActorID == "" {
		options.ActorID = DefaultActorID
	}
	if options.RowID == "" {
		options.RowID = newRowID()
	}

	deadline := time.Now().Add(options.Timeout)
	outcome := Outcome{RowID: options.RowID, SourceURL: options.SourceURL, Status: "unknown"}

	record := map[string]any{
		"rowId":     options.RowID,
		"sourceUrl": options.SourceURL,
		"actorId":   options.ActorID,
		"stage":     "start",
		"status":    "unknown",
		"updatedAt": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if options.RunID != "" {
		record["runId"] = options.RunID
		outcome.RunID = stringPtr(options.RunID)
	}
	if options.DatasetID != "" {
		record["datasetId"] = options.DatasetID
		outcome.DatasetID = stringPtr(options.DatasetID)
	}

	save := func(stage, status string, err error) {
		record["stage"] = stage
		record["status"] = status
		record["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
		if err != nil {
			record["error"] = SafeError(err)
		} else {
			delete(record, "error")
		}
		_ = options.Journal.Append(copyRecord(record))
	}

	fail := func(status, message string) Outcome {
		outcome.Status = status
		outcome.Error = message
		return outcome
	}

	datasetLimit := DefaultDatasetLimit
	if options.DatasetLimit != nil {
		datasetLimit = *options.DatasetLimit
	}
	if datasetLimit <= 0 {
		return fail("transport_error", "Dataset page limit must be a positive integer")
	}

	if options.RunID == "" {
		save("creating", "unknown", nil)
		actorPath := "/acts/" + strings.ReplaceAll(options.ActorID, "/", "~") + "/runs"
		if time.Now().After(deadline) {
			save("create_failed", "transport_error", errors.New("Overall run deadline exceeded before POST"))
			return fail("transport_error", "Overall run deadline exceeded before POST")
		}

		query := url.Values{}
		if options.MaxTotalChargeUSD != nil || options.Build != "" {
			query.Set("restartOnError", "false")
		}
		if options.MaxTotalChargeUSD != nil {
			query.Set("maxTotalChargeUsd", strconv.FormatFloat(*options.MaxTotalChargeUSD, 'f', -1, 64))
		}
		if options.Build != "" {
			query.Set("build", options.Build)
		}

		created, err := options.Transport.Request(ctx, "POST", actorPath, options.Input, query)
		if err != nil {
			apiErr := asApiError(err)
			if apiErr.Status == 429 {
				apiErr = &ApiError{
					Message: "HTTP 429 on Run creation; acceptance semantics are unverified; POST was not retried",
					Status:  429,
				}
			}
			if apiErr.Status == 429 || apiErr.Ambiguous || apiErr.Status == 0 {
				save("creation_unknown", "creation_unknown", apiErr)
				return fail("creation_unknown", SafeError(apiErr))
			}
			save("create_failed", "transport_error", apiErr)
			return fail("transport_error", SafeError(apiErr))
		}

		payload := asObject(created)
		runID, _ := payload["id"].(string)
		datasetID, _ := payload["defaultDatasetId"].(string)
		if runID == "" {
			save("creation_unknown", "creation_unknown", errors.New("POST response did not include a run ID"))
			return fail("creation_unknown", "POST response did not include a run ID")
		}
		record["runId"] = runID
		record["datasetId"] = datasetID
		outcome.RunID = stringPtr(runID)
		if datasetID != "" {
			outcome.DatasetID = stringPtr(datasetID)
		}
		save("run_created", "unknown", nil)

		if options.Build != "" {
			buildNumber := stringValue(payload["buildNumber"])
			buildID := stringValue(payload["buildId"])
			verification := "response"
			if buildNumber == "" || buildID == "" {
				verified, err := retryGet(ctx, options, deadline, "/actor-runs/"+runID)
				if err != nil {
					save("build_mismatch", "transport_error",
						fmt.Errorf("Could not verify actual Build identity: %s", SafeError(err)))
					outcome.Stage = "build_mismatch"
					outcome.BuildVerification = "failed"
					return fail("transport_error", "Could not verify actual Build identity")
				}
				verifiedPayload := asObject(verified)
				if buildNumber == "" {
					buildNumber = stringValue(verifiedPayload["buildNumber"])
				}
				if buildID == "" {
					buildID = stringValue(verifiedPayload["buildId"])
				}
				verification = "run_get"
			}
			if mismatch(options, buildNumber, buildID) {
				kind := mismatchKind(buildNumber, buildID)
				record["buildNumber"] = buildNumber
				record["buildId"] = buildID
				record["buildVerification"] = kind
				save("build_mismatch", "transport_error",
					errors.New("Actual Build identity is missing or does not match the requested Build"))
				outcome.Stage = "build_mismatch"
				outcome.BuildNumber = buildNumber
				outcome.BuildID = buildID
				outcome.BuildVerification = kind
				return fail("transport_error", "Actual Build identity is missing or does not match the requested Build")
			}
			record["buildNumber"] = buildNumber
			record["buildId"] = buildID
			record["buildVerification"] = verification
			save("build_verified", "unknown", nil)
		}
	} else {
		save("resuming", "unknown", nil)
	}

	for {
		if time.Now().After(deadline) {
			save("recoverable", "transport_error", errors.New("Overall run deadline exceeded"))
			return fail("transport_error", "Overall run deadline exceeded")
		}
		run, err := retryGet(ctx, options, deadline, "/actor-runs/"+*outcome.RunID)
		if err != nil {
			save("recoverable", "transport_error", err)
			return fail("transport_error", SafeError(err))
		}
		payload := asObject(run)

		if options.Build != "" && record["buildVerification"] == nil {
			buildNumber := stringValue(payload["buildNumber"])
			buildID := stringValue(payload["buildId"])
			if mismatch(options, buildNumber, buildID) {
				kind := mismatchKind(buildNumber, buildID)
				record["buildNumber"] = buildNumber
				record["buildId"] = buildID
				record["buildVerification"] = kind
				save("build_mismatch", "transport_error",
					errors.New("Run Build identity is missing or does not match the requested Build"))
				outcome.Stage = "build_mismatch"
				outcome.BuildNumber = buildNumber
				outcome.BuildID = buildID
				outcome.BuildVerification = kind
				return fail("transport_error", "Run Build identity is missing or does not match the requested Build")
			}
			record["buildNumber"] = buildNumber
			record["buildId"] = buildID
			record["buildVerification"] = "run_get"
			save("build_verified", "unknown", nil)
		}

		status := stringValue(payload["status"])
		if status == "SUCCEEDED" || status == "FAILED" || status == "TIMED-OUT" || status == "ABORTED" {
			if status != "SUCCEEDED" {
				save("run_finished", "run_failed", fmt.Errorf("Run ended with %s", status))
				return fail("run_failed", "Run ended with "+status)
			}
			runDatasetID := stringValue(payload["defaultDatasetId"])
			if outcome.DatasetID != nil && runDatasetID != "" && *outcome.DatasetID != runDatasetID {
				save("recoverable", "transport_error", errors.New("Journal Dataset ID does not match the Run response"))
				return fail("transport_error", "Journal Dataset ID does not match the Run response")
			}
			if outcome.DatasetID == nil && runDatasetID != "" {
				outcome.DatasetID = stringPtr(runDatasetID)
			}
			record["datasetId"] = stringValue(outcome.DatasetID)
			save("run_finished", "unknown", nil)
			break
		}

		remaining := time.Until(deadline)
		if remaining > 2*time.Second {
			options.Sleep(2 * time.Second)
		} else if remaining > 0 {
			options.Sleep(remaining)
		}
	}

	if outcome.DatasetID == nil || *outcome.DatasetID == "" {
		save("recoverable", "transport_error", errors.New("Run succeeded without a dataset ID"))
		return fail("transport_error", "Run succeeded without a dataset ID")
	}

	datasetPath := fmt.Sprintf("/datasets/%s/items?limit=%d&offset=0", *outcome.DatasetID, datasetLimit)
	items, err := retryGet(ctx, options, deadline, datasetPath)
	if err != nil {
		save("recoverable", "transport_error", err)
		return fail("transport_error", SafeError(err))
	}
	list, ok := items.([]any)
	if !ok {
		list = nil
	}

	// Dataset visibility can lag terminal Run status; confirm empty reads.
	for attempt := 0; attempt < 2; attempt++ {
		if len(list) != 0 {
			break
		}
		if time.Now().Add(time.Duration(attempt+1) * time.Second).After(deadline) {
			break
		}
		options.Sleep(time.Duration(attempt+1) * time.Second)
		items, err = retryGet(ctx, options, deadline, datasetPath)
		if err != nil {
			save("recoverable", "transport_error", err)
			return fail("transport_error", SafeError(err))
		}
		list, ok = items.([]any)
		if !ok {
			list = nil
		}
	}

	complete := len(list) < datasetLimit
	status, message := ClassifyItems(list)
	if !complete {
		status = "unknown"
		message = fmt.Sprintf("Dataset reached the %d-item page limit; completeness is unverified", datasetLimit)
	}
	save("complete", status, messageOrNil(message))

	outcome.Status = status
	outcome.Error = message
	outcome.RawItems = list
	outcome.DatasetComplete = boolPtr(complete)
	outcome.BuildNumber = stringValue(record["buildNumber"])
	outcome.BuildID = stringValue(record["buildId"])
	outcome.BuildVerification = stringValue(record["buildVerification"])
	return outcome
}

// retryGet is the only retrying path: 429, 5xx, or no response at all.
func retryGet(ctx context.Context, options RunOptions, deadline time.Time, path string) (any, error) {
	var last error
	for attempt := 0; attempt < options.MaxAttempts; attempt++ {
		if time.Now().After(deadline) {
			if last != nil {
				return nil, last
			}
			return nil, &ApiError{Message: "Overall run deadline exceeded"}
		}
		result, err := options.Transport.Request(ctx, "GET", path, nil, nil)
		if err == nil {
			return result, nil
		}
		last = err
		apiErr := asApiError(err)
		if !apiErr.retryable() || attempt+1 >= options.MaxAttempts {
			return nil, err
		}
		delay := apiErr.RetryAfter
		if delay <= 0 {
			delay = time.Duration(1<<attempt) * time.Second
			if delay > 8*time.Second {
				delay = 8 * time.Second
			}
		}
		if time.Now().Add(delay).After(deadline) {
			return nil, err
		}
		options.Sleep(delay)
	}
	return nil, last
}

func asApiError(err error) *ApiError {
	var apiErr *ApiError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return &ApiError{Message: SafeError(err)}
}

// asObject unwraps the {"data": {...}} envelope Apify uses for Run responses.
func asObject(value any) map[string]any {
	object, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	if nested, ok := object["data"].(map[string]any); ok {
		return nested
	}
	return object
}

func mismatch(options RunOptions, buildNumber, buildID string) bool {
	if buildNumber == "" || buildID == "" {
		return true
	}
	if buildNumber != options.Build {
		return true
	}
	return options.Build == ApprovedBuildNumber && buildID != ApprovedBuildID
}

func mismatchKind(buildNumber, buildID string) string {
	if buildNumber == "" && buildID == "" {
		return "missing"
	}
	return "mismatch"
}

func copyRecord(record map[string]any) map[string]any {
	copied := make(map[string]any, len(record))
	for key, value := range record {
		copied[key] = value
	}
	return copied
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	if number, ok := value.(float64); ok {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return fmt.Sprint(value)
}

func stringPtr(value string) *string { return &value }

func boolPtr(value bool) *bool { return &value }

func messageOrNil(message string) error {
	if message == "" {
		return nil
	}
	return errors.New(message)
}

func newRowID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("row-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

// Sanitize drops credential-like fields recursively before persisting raw data.
func Sanitize(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
			if isBlockedKey(normalized) {
				continue
			}
			cleaned[key] = Sanitize(item)
		}
		return cleaned
	case []any:
		cleaned := make([]any, 0, len(typed))
		for _, item := range typed {
			cleaned = append(cleaned, Sanitize(item))
		}
		return cleaned
	case string:
		return redactText(typed)
	default:
		return value
	}
}

func isBlockedKey(normalized string) bool {
	switch normalized {
	case "token", "sessionid", "cookie", "authorization", "password", "secret", "accesstoken", "refreshtoken", "apikey":
		return true
	}
	return strings.HasSuffix(normalized, "token") ||
		strings.HasSuffix(normalized, "secret") ||
		strings.HasSuffix(normalized, "password")
}
