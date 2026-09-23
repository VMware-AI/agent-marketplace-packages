package skillscmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// NewSkillsSignCmd creates `agentpkg skills sign`.
//
// Wraps `gpg --detach-sign --armor` on a skill zip. Produces a .asc
// file alongside the zip that consumers can verify with
// `gpg --verify hello-community-1.0.0.zip.asc hello-community-1.0.0.zip`.
//
// Mirrors `agentpkg package sign` byte-for-byte in behavior; the
// input/output paths differ but the gpg invocation is identical.
func NewSkillsSignCmd() *cobra.Command {
	var (
		keyID  string
		gpgBin string
		dryRun bool
	)
	c := &cobra.Command{
		Use:   "sign <zip>",
		Short: "GPG-detach-sign a skill zip (writes <zip>.asc)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			zip := args[0]
			if _, err := os.Stat(zip); err != nil {
				return fmt.Errorf("zip not found: %w", err)
			}
			outPath := zip + ".asc"
			gpgArgs := []string{"--batch", "--yes", "--armor", "--detach-sign", "--output", outPath}
			if keyID != "" {
				gpgArgs = append(gpgArgs, "--default-key", keyID)
			}
			gpgArgs = append(gpgArgs, zip)
			out := cmd.OutOrStdout()
			if dryRun {
				fmt.Fprintf(out, "(dry-run) gpg %v\n", gpgArgs)
				return nil
			}
			if _, err := os.Stat(gpgBin); err != nil {
				return fmt.Errorf("gpg not found at %s (install with apt install gnupg)", gpgBin)
			}
			gpgCmd := exec.Command(gpgBin, gpgArgs...)
			gpgCmd.Stdout = out
			gpgCmd.Stderr = out
			if err := gpgCmd.Run(); err != nil {
				return fmt.Errorf("gpg: %w", err)
			}
			fmt.Fprintf(out, "Signed %s -> %s\n", zip, filepath.Clean(outPath))
			return nil
		},
	}
	c.Flags().StringVar(&keyID, "key", "", "GPG key id / fingerprint (default: signing default key)")
	c.Flags().StringVar(&gpgBin, "gpg", "/usr/bin/gpg", "path to gpg binary")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print command without running")
	return c
}
