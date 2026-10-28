package cliargs

import (
	"reflect"
	"testing"
)

var valueFlags = map[string]bool{
	"out": true, "audio-format": true, "report": true, "timeout": true,
}

func TestReorderMovesTheInputLast(t *testing.T) {
	got := Reorder([]string{
		"https://www.facebook.com/share/r/1N1rvxbAse/",
		"-audio", "-audio-format", "m4a", "-out", "../../downloads/go", "-json",
	}, valueFlags)
	want := []string{
		"-audio", "-audio-format", "m4a", "-out", "../../downloads/go", "-json",
		"https://www.facebook.com/share/r/1N1rvxbAse/",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reorder =\n%v\nwant\n%v", got, want)
	}
}

func TestReorderKeepsOptionsFirstUnchanged(t *testing.T) {
	args := []string{"-out", "downloads", "-json", "https://www.facebook.com/reel/9/"}
	if got := Reorder(args, valueFlags); !reflect.DeepEqual(got, args) {
		t.Fatalf("Reorder = %v, want %v", got, args)
	}
}

func TestReorderLeavesInlineValuesAlone(t *testing.T) {
	got := Reorder([]string{"https://x", "-out=downloads", "-json"}, valueFlags)
	want := []string{"-out=downloads", "-json", "https://x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reorder = %v, want %v", got, want)
	}
}

func TestReorderTreatsDoubleDashAsSeparator(t *testing.T) {
	got := Reorder([]string{"-json", "--", "-weird-input"}, valueFlags)
	want := []string{"-json", "-weird-input"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reorder = %v, want %v", got, want)
	}
}
