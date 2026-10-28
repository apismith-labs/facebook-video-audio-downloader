// Package transcript is a small, dependency-free client for the Apify Actor
// apple_yang/facebook-transcript-scraper-api.
//
// It mirrors the Python and Node.js clients in this repository: the same eight
// statuses, the same exit codes, the same URL allow-list, and the same rule that
// Run creation is attempted exactly once.
package downloader

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Pinned build. See docs/audit/actor_audit_20261028.md before changing these.
const (
	ApprovedBuildNumber = "0.0.18"
	ApprovedBuildID     = "fZIlpYENof2jYqIJc"
	DefaultActorID      = "apple_yang/facebook-transcript-scraper-api"
	DefaultDatasetLimit = 100
	DefaultTimeout      = 900
)

// Statuses are the business outcomes a caller must handle explicitly.
var (
	Statuses = []string{
		"success", "actor_error", "empty", "no_transcript",
		"unknown", "transport_error", "run_failed", "creation_unknown",
	}
	ExitCodes = map[string]int{
		"success":          0,
		"actor_error":      4,
		"empty":            5,
		"no_transcript":    6,
		"unknown":          7,
		"transport_error":  8,
		"run_failed":       9,
		"creation_unknown": 10,
	}
)

// CLI-only exit codes, kept identical to the Python and Node.js clients.
const (
	ExitConfiguration    = 2
	ExitProtocol         = 3
	ExitBatchRowFailed   = 1
	ExitBatchConfigError = 2
)

var (
	allowedHosts = map[string]bool{
		"facebook.com":     true,
		"www.facebook.com": true,
		"m.facebook.com":   true,
		"web.facebook.com": true,
		"fb.watch":         true,
		"www.fb.watch":     true,
	}

	// First path segments that are site sections, never page slugs.
	reservedSegments = map[string]bool{
		"about": true, "business": true, "events": true, "fundraisers": true,
		"gaming": true, "groups": true, "help": true, "login": true,
		"login.php": true, "marketplace": true, "messages": true,
		"permalink.php": true, "photo": true, "plugins": true, "policies": true,
		"settings": true, "sharer": true, "stories": true, "story.php": true,
		"watchparty": true,
	}

	reWatch    = regexp.MustCompile(`(?i)^/watch/?$`)
	reVideoPHP = regexp.MustCompile(`(?i)^/video\.php$`)
	rePageVid  = regexp.MustCompile(`(?i)^/([A-Za-z0-9._-]+)/videos/(\d+)/?$`)
	reVideos   = regexp.MustCompile(`(?i)^/videos/(\d+)/?$`)
	reReel     = regexp.MustCompile(`(?i)^/reel/(\d+)/?$`)
	reShare    = regexp.MustCompile(`(?i)^/share/([vr])/([A-Za-z0-9_-]+)/?$`)
	reShort    = regexp.MustCompile(`^/([A-Za-z0-9_-]+)/?$`)
	reDigits   = regexp.MustCompile(`^\d+$`)
	reActorID  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/-]*$`)
	reBuild    = regexp.MustCompile(`^\d+(?:\.\d+){0,2}$`)
)

// ValidateVideoURL validates a public Facebook video URL and returns the
// canonical form plus a form label. Tracking parameters are dropped; only
// watch and video.php keep a query string, and only a single numeric v.
func ValidateVideoURL(raw string) (canonical string, form string, err error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return "", "", fmt.Errorf("url must be a bare Facebook video URL without surrounding whitespace")
	}
	if len(raw) > 2048 {
		return "", "", fmt.Errorf("url is longer than 2048 characters")
	}
	if strings.HasPrefix(strings.ToLower(raw), "http://") {
		return "", "", fmt.Errorf("url must use https://; http links are rejected")
	}
	parsed, parseErr := url.Parse(raw)
	if parseErr != nil {
		return "", "", fmt.Errorf("url could not be parsed")
	}
	if parsed.Scheme != "https" {
		return "", "", fmt.Errorf("url must be an absolute https:// Facebook video URL")
	}
	if parsed.User != nil {
		return "", "", fmt.Errorf("url must not contain user information")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", "", fmt.Errorf("url must not use a non-default port")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if !allowedHosts[host] {
		return "", "", fmt.Errorf("url host must be facebook.com, www.facebook.com, m.facebook.com, web.facebook.com, or fb.watch")
	}
	if parsed.Fragment != "" {
		return "", "", fmt.Errorf("url must not contain a fragment")
	}

	path := parsed.Path
	if path == "" {
		path = "/"
	}

	if host == "fb.watch" || host == "www.fb.watch" {
		m := reShort.FindStringSubmatch(path)
		if m == nil {
			return "", "", fmt.Errorf("fb.watch URLs must look like https://fb.watch/<code>/")
		}
		return "https://fb.watch/" + m[1] + "/", "short_link", nil
	}

	values, queryErr := url.ParseQuery(parsed.RawQuery)
	if queryErr != nil {
		return "", "", fmt.Errorf("url query string could not be parsed")
	}

	if reWatch.MatchString(path) {
		id, ok := singleV(values)
		if !ok {
			return "", "", fmt.Errorf("watch URLs must include exactly one numeric ?v= parameter")
		}
		return "https://www.facebook.com/watch/?v=" + id, "watch", nil
	}
	if reVideoPHP.MatchString(path) {
		id, ok := singleV(values)
		if !ok {
			return "", "", fmt.Errorf("video.php URLs must include exactly one numeric ?v= parameter")
		}
		return "https://www.facebook.com/video.php?v=" + id, "video_php", nil
	}
	if m := rePageVid.FindStringSubmatch(path); m != nil {
		slug := m[1]
		if reservedSegments[strings.ToLower(slug)] {
			return "", "", fmt.Errorf("url points at a site section, not a video")
		}
		return "https://www.facebook.com/" + slug + "/videos/" + m[2] + "/", "page_video", nil
	}
	if m := reVideos.FindStringSubmatch(path); m != nil {
		return "https://www.facebook.com/videos/" + m[1] + "/", "video", nil
	}
	if m := reReel.FindStringSubmatch(path); m != nil {
		return "https://www.facebook.com/reel/" + m[1] + "/", "reel", nil
	}
	if m := reShare.FindStringSubmatch(path); m != nil {
		return "https://www.facebook.com/share/" + strings.ToLower(m[1]) + "/" + m[2] + "/", "share", nil
	}
	return "", "", fmt.Errorf("unsupported Facebook URL form; see docs/input-output-fields.md#accepted-url-forms")
}

// singleV requires exactly one numeric v parameter; everything else is dropped.
func singleV(values url.Values) (string, bool) {
	all, present := values["v"]
	if !present || len(all) != 1 || !reDigits.MatchString(all[0]) {
		return "", false
	}
	return all[0], true
}

// ValidateBuild rejects "latest" and anything that is not an explicit numeric
// build. When required is true the build must equal the pinned number.
func ValidateBuild(value string, required bool) (string, error) {
	build := strings.TrimSpace(value)
	if build == "" {
		if required {
			return "", fmt.Errorf("a Build number is required when a Build must be pinned")
		}
		return "", nil
	}
	if !reBuild.MatchString(build) || strings.EqualFold(build, "latest") {
		return "", fmt.Errorf("build must be an explicit numeric Build number")
	}
	if required && build != ApprovedBuildNumber {
		return "", fmt.Errorf("build must be %s in this client", ApprovedBuildNumber)
	}
	return build, nil
}

// ValidateChargeCap accepts an optional positive USD cap written as a string.
func ValidateChargeCap(value string, required bool) (*float64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return nil, fmt.Errorf("APIFY_MAX_TOTAL_CHARGE_USD is required for capped test mode")
		}
		return nil, nil
	}
	var amount float64
	if _, err := fmt.Sscanf(trimmed, "%f", &amount); err != nil {
		return nil, fmt.Errorf("APIFY_MAX_TOTAL_CHARGE_USD must be a finite positive number")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("APIFY_MAX_TOTAL_CHARGE_USD must be a finite positive number")
	}
	return &amount, nil
}

// ValidateActorID keeps path traversal and whitespace out of the request path.
func ValidateActorID(value string) (string, error) {
	actor := strings.TrimSpace(value)
	if actor == "" {
		return "", fmt.Errorf("actor ID must not be empty")
	}
	if !reActorID.MatchString(actor) {
		return "", fmt.Errorf("actor ID contains unsupported characters")
	}
	return actor, nil
}
