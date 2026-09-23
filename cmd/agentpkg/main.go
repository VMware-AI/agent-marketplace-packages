// Command agentpkg is the dual-purpose CLI for the agent marketplace:
//
//   - VM-side: install / uninstall / upgrade an agent
//   - Package developer: scaffold / verify / build / sign / reindex a bundle
//
// It embeds the same apitypes and repo packages used by marketplace-api.
package main

import (
	"fmt"
	"os"

	"github.com/VMware-AI/agent-marketplace-packages/internal/cli"
	"github.com/VMware-AI/agent-marketplace-packages/internal/cli/packagecmd"
	"github.com/VMware-AI/agent-marketplace-packages/internal/cli/skillscmd"
	"github.com/spf13/cobra"
)

var (
	cfgPath     string
	credsPath   string
	dumpVersion bool
)

func main() {
	root := newRootCmd()
	root.AddCommand(cli.NewLoginCmd(&cfgPath, &credsPath))
	root.AddCommand(cli.NewLogoutCmd(&cfgPath, &credsPath))
	root.AddCommand(cli.NewWhoamiCmd(&cfgPath, &credsPath))
	root.AddCommand(cli.NewIndexCmd(&cfgPath, &credsPath))
	root.AddCommand(cli.NewShowCmd(&cfgPath, &credsPath))
	root.AddCommand(cli.NewDownloadCmd(&cfgPath, &credsPath))
	root.AddCommand(cli.NewConfigCmd(&cfgPath, &credsPath))
	root.AddCommand(cli.NewInstallCmd(&cfgPath, &credsPath))
	root.AddCommand(cli.NewUpgradeCmd(&cfgPath, &credsPath))
	root.AddCommand(cli.NewRollbackCmd(&cfgPath, &credsPath))
	root.AddCommand(cli.NewUninstallCmd(&cfgPath, &credsPath))

	root.AddCommand(newPackageCmd())
	root.AddCommand(newSkillsCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agentpkg",
		Short: "agent marketplace CLI (VM install + package authoring)",
		Long: `agentpkg is a single binary used both as a VM-side installer and as
a package author toolkit. After running 'agentpkg login', the same binary
can fetch, verify, and install agent tarballs from a configured marketplace.

Subcommand groups:
  VM install:        login, logout, whoami, index, show, download,
                     install, upgrade, rollback, uninstall
  Package authoring: package init, package verify, package build,
                     package reindex, package sign`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       "0.1.0",
	}

	c.PersistentFlags().StringVar(&cfgPath, "config", defaultConfigPath(),
		"path to agentpkg config.yaml")
	c.PersistentFlags().StringVar(&credsPath, "credentials", defaultCredentialsPath(),
		"path to agentpkg credentials (Basic Auth password)")
	c.Flags().BoolVar(&dumpVersion, "version", false, "print version and exit")
	c.SetVersionTemplate("agentpkg {{.Version}}\n")

	return c
}

func defaultConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home + "/.config/agentpkg/config.yaml"
	}
	return "config.yaml"
}

func defaultCredentialsPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home + "/.config/agentpkg/credentials"
	}
	return "credentials"
}

// newPackageCmd returns the `package` subcommand group with init/verify/
// build/reindex/sign attached. Renamed internally to `packagecmd` to
// avoid colliding with the Go keyword.
func newPackageCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "package",
		Short: "Package authoring tools (scaffold, verify, build, sign, reindex)",
		Long: `package contains the commands used by package authors to
scaffold new agent directories, validate bundles, build tarballs, GPG-sign
them, and refresh dist/index.json.`,
	}
	c.AddCommand(packagecmd.NewInitCmd())
	c.AddCommand(packagecmd.NewVerifyCmd())
	c.AddCommand(packagecmd.NewBuildCmd())
	c.AddCommand(packagecmd.NewReindexCmd())
	c.AddCommand(packagecmd.NewSignCmd())
	return c
}

// newSkillsCmd returns the `skills` subcommand group. Implementation lives
// in internal/cli/skillscmd so the wiring (init/build/verify/sign for
// authors; list/show/download/upload/delete for consumers; install/
// uninstall/list-installed for the local install layout) is colocated
// with the command logic. See skillscmd.NewSkillsCmd for the full
// command list.
func newSkillsCmd() *cobra.Command {
	return skillscmd.NewSkillsCmd(&cfgPath, &credsPath)
}
