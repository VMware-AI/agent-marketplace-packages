package packagecmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// NewSignCmd creates `agentpkg package sign`.
//
// Wraps `gpg --detach-sign --armor` on a tarball. Produces a .sig file
// alongside the tarball that consumers can verify.
func NewSignCmd() *cobra.Command {
	var (
		keyID  string
		gpgBin string
		dryRun bool
	)
	c := &cobra.Command{
		Use:   "sign <tarball>",
		Short: "GPG-detach-sign a tarball (writes <tarball>.asc)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tarball := args[0]
			if _, err := os.Stat(tarball); err != nil {
				return fmt.Errorf("tarball not found: %w", err)
			}
			outPath := tarball + ".asc"
			args2 := []string{"--batch", "--yes", "--armor", "--detach-sign", "--output", outPath}
			if keyID != "" {
				args2 = append(args2, "--default-key", keyID)
			}
			args2 = append(args2, tarball)
			if dryRun {
				fmt.Printf("(dry-run) gpg %v\n", args2)
				return nil
			}
			// Only check gpg exists when actually running — dry-run is for
			// previewing the command on a host that may not have gpg.
			if _, err := os.Stat(gpgBin); err != nil {
				return fmt.Errorf("gpg not found at %s (install with apt install gnupg)", gpgBin)
			}
			gpgCmd := exec.Command(gpgBin, args2...)
			gpgCmd.Stdout = os.Stdout
			gpgCmd.Stderr = os.Stderr
			if err := gpgCmd.Run(); err != nil {
				return fmt.Errorf("gpg: %w", err)
			}
			fmt.Printf("Signed %s -> %s\n", tarball, filepath.Clean(outPath))
			return nil
		},
	}
	c.Flags().StringVar(&keyID, "key", "", "GPG key id / fingerprint (default: signing default key)")
	c.Flags().StringVar(&gpgBin, "gpg", "/usr/bin/gpg", "path to gpg binary")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print command without running")
	return c
}
