// Package dotenv loads APIFY_* settings from the nearest .env file.
//
// The Python and Node clients resolve .env from the repository root. A Go binary has no such
// anchor, so Load walks up from the working directory; without that, `go run ./cmd/download` from
// examples/go would report a configuration error even though the root .env exists.
package dotenv

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// MaxDepth bounds the upward search so a stray .env in a parent directory is not picked up.
const MaxDepth = 4

// Load reads the nearest .env, skipping the search entirely when APIFY_SKIP_DOTENV is set.
func Load() {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APIFY_SKIP_DOTENV"))) {
	case "1", "true", "yes":
		return
	}
	directory, err := os.Getwd()
	if err != nil {
		return
	}
	for depth := 0; depth <= MaxDepth; depth++ {
		candidate := filepath.Join(directory, ".env")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			read(candidate)
			return
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return
		}
		directory = parent
	}
}

// read applies APIFY_* lines without overwriting values already present in the environment.
func read(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if strings.HasPrefix(key, "APIFY_") {
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, value)
			}
		}
	}
}
