package downloader

import (
	"math"
	"testing"
)

func TestEstimateCostIsFlatPerResult(t *testing.T) {
	cases := []struct {
		videos int
		want   float64
	}{
		{0, 0},
		{1, 0.005},
		{1000, 5.00},
	}

	for _, testCase := range cases {
		got, err := EstimateCost(testCase.videos)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if math.Abs(got-testCase.want) > 1e-9 {
			t.Fatalf("%d video(s) = %.6f, want %.6f", testCase.videos, got, testCase.want)
		}
	}
}

func TestEstimateCostRejectsNegativeCount(t *testing.T) {
	if _, err := EstimateCost(-1); err == nil {
		t.Fatal("a negative video count must fail")
	}
}

func TestFormatUSD(t *testing.T) {
	if got := FormatUSD(0.005); got != "$0.0050" {
		t.Fatalf("FormatUSD = %q", got)
	}
}
