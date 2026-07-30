package server

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// ParseLevel parses one of "debug|info|warn|error" into slog.Level.
// Unknown / empty returns (slog.LevelInfo, false) — callers should WARN
// and use info as a safe default.
func ParseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info", "":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error", "err":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

// ParseFormat parses one of "text|json". Unknown / empty returns ("text", false).
func ParseFormat(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text":
		return "text", true
	case "json":
		return "json", true
	default:
		return "text", false
	}
}

// NewLogger builds a slog.Logger that writes to stdout (required, container-friendly)
// plus an optional file (append, 0644) when logFile != "".
//
// The format is "text" (default) or "json". Returns a close function the caller
// should defer — it closes the file writer if one was opened.
func NewLogger(level, format, logFile string) (*slog.Logger, func() error, error) {
	lvl, lvlOK := ParseLevel(level)
	formatName, formatOK := ParseFormat(format)

	var writers []io.Writer
	writers = append(writers, os.Stdout)

	var file *os.File
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file %q: %w", logFile, err)
		}
		file = f
		writers = append(writers, f)
	}

	mw := io.MultiWriter(writers...)

	handlerOpts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if formatName == "json" {
		handler = slog.NewJSONHandler(mw, handlerOpts)
	} else {
		handler = slog.NewTextHandler(mw, handlerOpts)
	}

	logger := slog.New(handler)

	closeFn := func() error {
		if file != nil {
			return file.Close()
		}
		return nil
	}

	if !lvlOK {
		logger.Warn("unknown log level, falling back to info", "got", level)
	}
	if !formatOK {
		logger.Warn("unknown log format, falling back to text", "got", format)
	}

	return logger, closeFn, nil
}