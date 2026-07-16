package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewLogoutCmd creates `agentpkg logout`.
func NewLogoutCmd(cfgPath, credsPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove saved credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			removed := false
			if err := os.Remove(*credsPath); err == nil {
				removed = true
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("remove credentials: %w", err)
			}
			if removed {
				fmt.Println("Logged out.")
			} else {
				fmt.Println("Already logged out.")
			}
			return nil
		},
	}
}