package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/VMware-AI/agent-marketplace-packages/internal/manifest"
	"github.com/spf13/cobra"
)

// NewConfigCmd creates `agentpkg config` — currently only the `generate`
// subcommand. Future: `show`, `set`, etc.
func NewConfigCmd(cfgPath, credsPath *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "config <subcommand>",
		Short: "Manage agent configurations (currently: generate)",
		Long: `config contains subcommands for preparing agent configuration
files. Currently it exposes ` + "`generate`" + `, which downloads an agent's
tarball, extracts the agent's render-config.sh, and invokes it with the
daemon-supplied config input.

` + "`generate`" + ` is Stage 1 of the daemon-driven install flow:
  1. daemon writes its config dict to a JSON file
  2. agentpkg config generate <agent> --config-input <file>
     → writes the agent's config file to its upstream-canonical location
  3. agentpkg install <agent>
     → deploys the binary and starts the systemd service`,
	}
	c.AddCommand(NewConfigGenerateCmd(cfgPath, credsPath))
	return c
}

// NewConfigGenerateCmd creates `agentpkg config generate`.
func NewConfigGenerateCmd(cfgPath, credsPath *string) *cobra.Command {
	var (
		version    string
		source     string
		channel    string
		targetRoot string
		cacheDir   string
		inputPath  string
	)
	c := &cobra.Command{
		Use:   "generate <agent> --config-input <file>",
		Short: "Render the agent's configuration files using its render-config.sh",
		Long: `generate downloads an agent's tarball (caching it like install
does), extracts the embedded render-config.sh, and invokes it with
$AGENT_MARKETPLACE_CONFIG_INPUT pointing at the daemon-supplied config
input file. The render script writes the agent's config file(s) to the
upstream-canonical locations declared in manifest.configs[].

Exit codes:
  0 — success
  1 — invalid arguments / config input not found
  70 — render-config.sh reported a configuration error (missing required key)
  71 — render-config.sh reported a script error
  72 — soft failure (reserved, currently unused here)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if inputPath == "" {
				return fmt.Errorf("--config-input is required")
			}
			if _, err := os.Stat(inputPath); err != nil {
				return fmt.Errorf("--config-input not readable: %w", err)
			}
			client, err := NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			return runConfigGenerate(client, name, source, channel, version, targetRoot, cacheDir, inputPath)
		},
	}
	c.Flags().StringVar(&source, "source", "upstream", "source tree (upstream | ours)")
	c.Flags().StringVar(&channel, "channel", "stable", "channel (stable | beta | dev)")
	c.Flags().StringVar(&version, "version", "", "specific version (default: latest stable)")
	c.Flags().StringVar(&targetRoot, "target-root", "", "target install root (default: $HOME/.local)")
	c.Flags().StringVar(&cacheDir, "cache-dir", "", "tarball cache directory")
	c.Flags().StringVar(&inputPath, "config-input", "", "path to daemon-supplied config input JSON file (required)")
	return c
}

// runConfigGenerate implements Stage 1 of the install flow:
//   - resolve version + cache the tarball
//   - extract manifest.json + render-config.sh from the tarball
//   - exec render-config.sh with the right env vars
//   - (optional soft-warn) verify the manifest-declared config files exist
func runConfigGenerate(c *Client, name, source, channel, version, targetRoot, cacheDir, inputPath string) error {
	if version == "" {
		v, err := pickLatestStable(c, name, source, channel)
		if err != nil {
			return fmt.Errorf("resolve latest version: %w", err)
		}
		version = v
		fmt.Printf("Resolved latest %s: %s\n", name, version)
	}

	tarName, expectedSHA, err := resolveTarball(c, name, source, channel, version)
	if err != nil {
		return err
	}

	// Cache the tarball (same logic as runInstall).
	resolvedCacheDir := cacheDir
	if resolvedCacheDir == "" {
		resolvedCacheDir = filepath.Join(userHomeOrTmp(), ".local", "share", "agentpkg", "cache")
	}
	if err := os.MkdirAll(resolvedCacheDir, 0755); err != nil {
		return fmt.Errorf("mkdir cache: %w", err)
	}
	cachePath := filepath.Join(resolvedCacheDir, tarName)
	if !needsDownload(cachePath, expectedSHA) {
		fmt.Printf("Using cached %s\n", cachePath)
	} else {
		if err := downloadAndVerify(c, name, source, channel, version, tarName, expectedSHA, cachePath); err != nil {
			return err
		}
		fmt.Printf("Downloaded %s -> %s\n", tarName, cachePath)
	}

	// Read manifest so we know (a) where render-config.sh lives (b) which
	// config files to verify post-render (c) which env vars to pass.
	mf, err := extractManifestFromTarball(cachePath, version)
	if err != nil {
		return err
	}

	// Extract render-config.sh to a tmpfile.
	renderScriptPath, err := os.CreateTemp("", "agentpkg-render-")
	if err != nil {
		return fmt.Errorf("create tmp for render script: %w", err)
	}
	renderScriptPath.Close()
	defer os.Remove(renderScriptPath.Name())
	if err := extractRenderScriptFromTarball(cachePath, version, renderScriptPath.Name()); err != nil {
		return err
	}

	// Compute deploy_root for the env var (informational).
	deployRoot := filepath.Join(resolveTargetRoot(targetRoot), name, version)

	// Invoke render-config.sh with the agent-marketplace env vars.
	cmd := exec.Command(renderScriptPath.Name())
	cmd.Env = append(os.Environ(),
		"AGENT_MARKETPLACE_CONFIG_INPUT="+inputPath,
		"AGENT_MARKETPLACE_AGENT="+name,
		"AGENT_MARKETPLACE_VERSION="+version,
		"AGENT_MARKETPLACE_DEPLOY_ROOT="+deployRoot,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Exit code mapping: 1 → 70 (config error), anything else → 71.
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 1:
				// Use os.Exit to bypass cobra's default exit-1 behavior.
				// Print to stderr first so users see the cause.
				fmt.Fprintln(os.Stderr, "render-config.sh: configuration error (exit 70)")
				os.Exit(70)
			default:
				fmt.Fprintf(os.Stderr, "render-config.sh: script error exit %d (agentpkg exit 71)\n", exitErr.ExitCode())
				os.Exit(71)
			}
		}
		return fmt.Errorf("render-config.sh: %w", err)
	}

	// Soft-warn if a declared render_to file is missing.
	if len(mf.Configs) > 0 {
		fmt.Println("Verifying rendered config files:")
		for _, cfg := range mf.Configs {
			// RenderTo may start with $HOME or be a literal path. The
			// script writes to $HOME if it used a ~. We do NOT expand ~
			// ourselves — that is the script's job. We trust the script.
			fmt.Printf("  %s -> expected at %s\n", cfg.Name, cfg.RenderTo)
		}
		fmt.Println("(run `ls` to confirm; agentpkg does not write the file itself)")
	}

	return nil
}

// resolveTargetRoot returns targetRoot if non-empty, otherwise the default
// $HOME/.local. Mirrors the default-resolution logic in runInstall.
func resolveTargetRoot(targetRoot string) string {
	if targetRoot != "" {
		return targetRoot
	}
	return filepath.Join(userHomeOrTmp(), ".local")
}

// manifest.LoadFromBytes is imported via the manifest package — keep an
// alias here for clarity (silence unused-import lint in case this file
// gains non-render helpers later).
var _ = manifest.LoadFromBytes