package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewWhoamiCmd creates `agentpkg whoami`.
func NewWhoamiCmd(cfgPath, credsPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the current marketplace server + a quick health check",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			var health struct {
				Status  string `json:"status"`
				Version string `json:"version"`
			}
			if err := c.do("/api/v1/health", &health); err != nil {
				return err
			}
			fmt.Printf("Server: %s\n", c.BaseURL)
			fmt.Printf("Status: %s (api version: %s)\n", health.Status, health.Version)
			return nil
		},
	}
}
