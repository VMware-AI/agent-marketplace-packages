package cli

import (
	"errors"
	"fmt"
)

// Exit codes used by agentpkg. Numbers 0..72 are inherited from install.sh
// and render-config.sh (see docs/install-protocol.md and docs/agentpkg.md).
// Numbers 73..79 are reserved for VM-side orchestration signals. The "1" in
// cmd/agentpkg/main.go:41 stays as the catch-all for unspecified errors.
//
// 73 is intentionally skipped — left open so a future "rollback command
// itself was malformed" code can be added without renumbering.
const (
	ExitOK = 0

	ExitGeneric = 1 // unspecified error from main()

	// install.sh semantic codes (also raised by uninstall.sh).
	ExitSameVersion       = 10
	ExitPlatformMismatch  = 20
	ExitManifestInvalid   = 30
	ExitSystemToolMissing = 40
	ExitChecksumMismatch  = 50
	ExitPostInstallVerify = 60

	// render-config.sh semantic codes (mapped via mapRenderExitToCLI).
	ExitRenderMissing     = 70
	ExitRenderScriptError = 71

	// systemd --user unavailable (soft — install still succeeds).
	ExitSystemdUnavailable = 72

	// --- rollback-specific (73..79) ---
	ExitRollbackNoTarget      = 74 // state.json missing or has no .previous block
	ExitRollbackTargetUnknown = 75 // target version not present in the index
	ExitRollbackDownloadFail  = 76 // tarball download or sha256 verify failure

	// --- CLI-level usage / config errors (60..69 reserved) ---
	ExitUsageError = 64 // EX_USAGE: bad CLI invocation (e.g. install --dry-run)
)

// ExitCoder is the interface errors implement to override the default
// exit-code-1 handling in main.go. Returning a typed error with an
// explicit code lets a command distinguish "real failure" (1) from
// "wrong invocation" (64), "no rollback target" (74), etc. main.go's
// error path checks for this via errors.As.
//
// Both *rollbackError (internal/cli/install.go) and *exitCodeError (below)
// satisfy this interface. The render-config.sh path uses os.Exit directly
// instead because it owns the sub-process ExitError.
type ExitCoder interface {
	error
	ExitCode() int
}

// exitCodeError is the generic typed exit code used by commands that want
// to signal a specific exit code from RunE without needing a dedicated
// error type (e.g. "wrong flag combination"). Implements ExitCoder so
// main.go can read the code via errors.As.
type exitCodeError struct {
	code int
	msg  string
}

func (e *exitCodeError) Error() string { return e.msg }
func (e *exitCodeError) ExitCode() int  { return e.code }

// ExitErrorf builds an exitCodeError with a formatted message.
func ExitErrorf(code int, format string, args ...any) error {
	return &exitCodeError{code: code, msg: fmt.Sprintf(format, args...)}
}

// FormatExitError renders an error for stderr. If the error is an
// ExitCoder it prefixes the code in brackets; otherwise just the message.
// Used by main.go so the user sees "[64]: ..." style prefixes that match
// what shell wrappers will receive as $?.
//
// Splitting this out from main.go keeps the formatting logic testable.
func FormatExitError(err error) string {
	if err == nil {
		return ""
	}
	var ec ExitCoder
	if errorsAs(err, &ec) {
		return fmt.Sprintf("[exit %d] %s", ec.ExitCode(), err.Error())
	}
	return err.Error()
}

// errorsAs is a tiny shim around errors.As to avoid the import in this
// frequently-included file. Kept as a free function so callers can
// always reach for it.
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}