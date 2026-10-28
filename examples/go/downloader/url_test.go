package downloader

import "testing"

func TestValidateVideoURLAcceptedForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		canonical string
		form      string
	}{
		{"watch", "https://www.facebook.com/watch/?v=1234567890", "https://www.facebook.com/watch/?v=1234567890", "watch"},
		{"watch without slash", "https://facebook.com/watch?v=1234567890", "https://www.facebook.com/watch/?v=1234567890", "watch"},
		{"watch drops tracking", "https://www.facebook.com/watch/?v=1234567890&mibextid=abc&rdid=xyz", "https://www.facebook.com/watch/?v=1234567890", "watch"},
		{"page video", "https://www.facebook.com/SomePage/videos/987654321/", "https://www.facebook.com/SomePage/videos/987654321/", "page_video"},
		{"videos path", "https://m.facebook.com/videos/1234567890", "https://www.facebook.com/videos/1234567890/", "video"},
		{"reel", "https://web.facebook.com/reel/1234567890/", "https://www.facebook.com/reel/1234567890/", "reel"},
		{"video php", "https://www.facebook.com/video.php?v=1234567890", "https://www.facebook.com/video.php?v=1234567890", "video_php"},
		{"share v", "https://www.facebook.com/share/v/AbCdEf123/", "https://www.facebook.com/share/v/AbCdEf123/", "share"},
		{"share r", "https://www.facebook.com/share/R/AbCdEf123", "https://www.facebook.com/share/r/AbCdEf123/", "share"},
		{"short link", "https://fb.watch/AbCdEf123/", "https://fb.watch/AbCdEf123/", "short_link"},
		{"short link without slash", "https://www.fb.watch/AbCdEf123", "https://fb.watch/AbCdEf123/", "short_link"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			canonical, form, err := ValidateVideoURL(testCase.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if canonical != testCase.canonical {
				t.Fatalf("canonical = %q, want %q", canonical, testCase.canonical)
			}
			if form != testCase.form {
				t.Fatalf("form = %q, want %q", form, testCase.form)
			}
		})
	}
}

func TestValidateVideoURLRejectedForms(t *testing.T) {
	cases := map[string]string{
		"http":             "http://www.facebook.com/watch/?v=123",
		"fragment":         "https://www.facebook.com/watch/?v=123#top",
		"stories":          "https://www.facebook.com/stories/123/",
		"groups":           "https://www.facebook.com/groups/123/posts/456/",
		"photo":            "https://www.facebook.com/photo/?fbid=123",
		"unknown host":     "https://example.com/watch/?v=123",
		"userinfo":         "https://user:pass@www.facebook.com/watch/?v=123",
		"non-default port": "https://www.facebook.com:8443/watch/?v=123",
		"no v":             "https://www.facebook.com/watch/",
		"two v":            "https://www.facebook.com/watch/?v=1&v=2",
		"non numeric v":    "https://www.facebook.com/watch/?v=abc",
		"share x":          "https://www.facebook.com/share/x/abc/",
		"short bad path":   "https://fb.watch/a/b/",
		"relative":         "/watch/?v=123",
		"markdown wrapped": "[video](https://www.facebook.com/watch/?v=123)",
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ValidateVideoURL(input); err == nil {
				t.Fatalf("expected rejection for %q", input)
			}
		})
	}
}

func TestValidateVideoURLWhitespaceAndLength(t *testing.T) {
	if _, _, err := ValidateVideoURL(" https://www.facebook.com/watch/?v=123"); err == nil {
		t.Fatal("leading whitespace must be rejected")
	}
	long := "https://www.facebook.com/watch/?v=123&x=" + stringsOfLength(2100)
	if _, _, err := ValidateVideoURL(long); err == nil {
		t.Fatal("over-long URL must be rejected")
	}
}

func TestValidateBuild(t *testing.T) {
	if _, err := ValidateBuild("latest", false); err == nil {
		t.Fatal("latest must be rejected")
	}
	if _, err := ValidateBuild("0.0.19", true); err == nil {
		t.Fatal("a different build must be rejected when required")
	}
	if build, err := ValidateBuild("0.0.18", true); err != nil || build != ApprovedBuildNumber {
		t.Fatalf("pinned build should pass, got %q %v", build, err)
	}
}

func TestValidateChargeCap(t *testing.T) {
	if _, err := ValidateChargeCap("0", false); err == nil {
		t.Fatal("zero cap must be rejected")
	}
	if _, err := ValidateChargeCap("-1", false); err == nil {
		t.Fatal("negative cap must be rejected")
	}
	if _, err := ValidateChargeCap("", true); err == nil {
		t.Fatal("an explicit requirement must reject an empty cap")
	}
	value, err := ValidateChargeCap("0.10", false)
	if err != nil || value == nil || *value != 0.10 {
		t.Fatalf("0.10 should pass, got %v %v", value, err)
	}
}

func stringsOfLength(length int) string {
	buffer := make([]byte, length)
	for index := range buffer {
		buffer[index] = 'a'
	}
	return string(buffer)
}
