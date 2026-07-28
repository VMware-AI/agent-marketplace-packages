package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// readLineFrom reads one line (until \n) from r. Strips the trailing \n.
func readLineFrom(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	// strip trailing \n / \r
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line, nil
}

// readAll reads everything from r. Convenience wrapper.
func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// jsonMarshalIndent is a thin wrapper around encoding/json for tests and
// helpers that don't want to import encoding/json directly.
func jsonMarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

// writeFile writes data to path with mode. Parent directory must exist.
func writeFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}

// errSilent is used when a command wants to fail without printing the
// cobra usage hint (we already set SilenceUsage at the root).
func errSilent(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
