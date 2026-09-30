package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewLogoutCmd creates `agentpkg logout`.
//
// F001 (tested 2026-09-30): the default `logout` only removes the
// credentials file, leaving config.yaml (server URL, skip_cert_verify,
// ca_cert) intact. That is deliberate — operators re-login frequently
// (same machine, same server) and don't want to re-type
// --server / --skip-cert-verify every time. But the previous behavior
// was undocumented, and there's no opt-out for the "I want a clean
// slate" case.
//
// We now expose --clear-config as the explicit "wipe everything" knob
// and document the default in the long help. logout without flags
// behaves exactly as before — credentials gone, server settings
// preserved.
func NewLogoutCmd(cfgPath, credsPath *string) *cobra.Command {
	var clearConfig bool
	c := &cobra.Command{
		Use:   "logout [--clear-config]",
		Short: "Remove saved credentials (server URL and TLS settings preserved by default)",
		Long: `logout removes the saved credentials file. By default the
server URL and TLS settings in config.yaml are preserved so a subsequent
'agentpkg login' doesn't need --server / --skip-cert-verify / --ca-cert
flags again.

Pass --clear-config to wipe config.yaml as well — for a full reset
to first-time state.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			removed := false
			if err := os.Remove(*credsPath); err == nil {
				removed = true
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("remove credentials: %w", err)
			}
			if removed {
				fmt.Fprintln(out, "Logged out (credentials removed).")
			} else {
				fmt.Fprintln(out, "Already logged out.")
			}
			if clearConfig {
				// Treat missing config as success — logout --clear-config
				// is "ensure no config either way". Same for an
				// already-empty file (idempotent).
				if err := os.Remove(*cfgPath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove config: %w", err)
				}
				fmt.Fprintln(out, "Cleared config.yaml.")
			} else {
				fmt.Fprintf(out, "Server / TLS settings preserved in %s (pass --clear-config to wipe).\n", *cfgPath)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&clearConfig, "clear-config", false, "also remove config.yaml (server URL + TLS settings); default preserves them so re-login is fast")
	return c
}