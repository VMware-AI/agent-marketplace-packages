package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestParseLevel covers valid + invalid level strings.
func TestParseLevel(t *testing.T) {
	cases := []struct {
		in       string
		want     slog.Level
		ok       bool
	}{
		{"debug", slog.LevelDebug, true},
		{"DEBUG", slog.LevelDebug, true},
		{" info ", slog.LevelInfo, true},
		{"", slog.LevelInfo, true},
		{"warn", slog.LevelWarn, true},
		{"warning", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"err", slog.LevelError, true},
		{"trace", slog.LevelInfo, false},
		{"verbose", slog.LevelInfo, false},
	}
	for _, tc := range cases {
		got, ok := ParseLevel(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseLevel(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestParseFormat covers valid + invalid format strings.
func TestParseFormat(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"text", "text", true},
		{"", "text", true},
		{"JSON", "json", true},
		{"yaml", "text", false},
	}
	for _, tc := range cases {
		got, ok := ParseFormat(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseFormat(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestLogger_FilterByLevel verifies that a logger built at level X
// drops records below X.
func TestLogger_FilterByLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	logger.Info("should-be-dropped")
	logger.Warn("should-appear")
	logger.Error("should-appear")

	out := buf.String()
	if strings.Contains(out, "should-be-dropped") {
		t.Errorf("Info-level record not filtered: %q", out)
	}
	if !strings.Contains(out, "should-appear") {
		t.Errorf("Warn/Error records not emitted: %q", out)
	}
}