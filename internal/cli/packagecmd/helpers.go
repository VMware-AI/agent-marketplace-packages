package packagecmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// computeFileSHA256 returns the hex sha256 of a file (no prefix).
func computeFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// mustFileMode returns os.FileMode parsed from a string, defaulting to 0644.
func mustFileMode(s string) os.FileMode {
	m, err := parseFileMode(s)
	if err != nil {
		return 0644
	}
	return m
}

// parseFileMode parses an octal file-mode string.
func parseFileMode(s string) (os.FileMode, error) {
	if s == "" {
		return 0, fmt.Errorf("empty mode")
	}
	var m uint64
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0, fmt.Errorf("invalid octal: %s", s)
		}
		m = m*8 + uint64(c-'0')
	}
	return os.FileMode(m), nil
}
