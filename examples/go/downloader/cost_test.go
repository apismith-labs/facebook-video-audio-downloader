package downloader

import (
	"math"
	"testing"
)

func TestEstimateCostMatchesDocumentedTable(t *testing.T) {
	cases := []struct {
		seconds float64
		tier    string
		want    float64
	}{
		{30, "FREE", 0.0055},
		{60, "FREE", 0.0055},
		{600, "FREE", 0.0460},
		{1800, "FREE", 0.1360},
		{3600, "FREE", 0.2710},
		{30, "GOLD", 0.0030},
		{600, "GOLD", 0.0210},
	}

	for _, testCase := range cases {
		got, err := EstimateCost(1, testCase.seconds, testCase.tier)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if math.Abs(got-testCase.want) > 1e-9 {
			t.Fatalf("%.0fs on %s = %.6f, want %.6f", testCase.seconds, testCase.tier, got, testCase.want)
		}
	}
}

func TestEstimateCostUnknownTier(t *testing.T) {
	if _, err := EstimateCost(1, 60, "PLATINUM-PLUS"); err == nil {
		t.Fatal("an unknown tier must fail")
	}
}

func TestEstimateCostThousandOneMinuteVideos(t *testing.T) {
	got, err := EstimateCost(1000, 60, "FREE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(got-5.50) > 1e-9 {
		t.Fatalf("cost = %.4f, want 5.50", got)
	}
}

func TestBilledMinutes(t *testing.T) {
	cases := map[float64]int{
		0:    1,
		1:    1,
		59.9: 1,
		60:   1,
		60.1: 2,
		90:   2,
		120:  2,
		121:  3,
	}
	for seconds, want := range cases {
		if got := BilledMinutes(seconds); got != want {
			t.Fatalf("BilledMinutes(%.1f) = %d, want %d", seconds, got, want)
		}
	}
}

func TestFormatUSD(t *testing.T) {
	if got := FormatUSD(0.046); got != "$0.0460" {
		t.Fatalf("FormatUSD = %q", got)
	}
}
