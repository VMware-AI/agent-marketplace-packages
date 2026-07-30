package cli

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
)