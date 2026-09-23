package skillscmd

// cfgPath and credsPath are package-level variables populated by
// NewSkillsCmd at construction time. They are shared across all
// skillscmd commands that need them (list/show/download/upload/
// delete/install) — author-side commands (init/verify/build/sign)
// and install-side (uninstall/list-installed) don't touch them.
//
// Mirrors the package-init pattern in internal/cli (which has
// package-level cfgPath/credsPath from cmd/agentpkg/main.go). Putting
// them in package vars keeps every command constructor short — instead
// of threading two pointers through 12 constructors.
var (
	cfgPath   *string
	credsPath *string
)

// SetGlobals populates the package-level cfgPath/credsPath from the
// agentpkg root command. Called once from cmd/agentpkg/main.go when
// the skills subcommand group is registered. Subsequent calls (in
// tests) are fine because each test re-sets before running.
func SetGlobals(cfg, creds *string) {
	cfgPath = cfg
	credsPath = creds
}