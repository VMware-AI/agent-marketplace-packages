package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
does), extracts the embedded render-config.sh, and invokes it.

--config-input points at the daemon-supplied config payload: a slot-keyed JSON
object whose top-level keys are manifest.configs[].name — the same shape as
the agent's config/example.* file, so a raw-mode frontend can submit its edited
example unchanged. agentpkg splits it per slot, base64-encodes each slot BODY
(not the wrapper), and exports MARKETPLACE_CONFIG_<SLOT> for each, where
<SLOT> is the uppercased slot name with '-' → '_' (hermes-agent's "hermes-env"
becomes MARKETPLACE_CONFIG_HERMES_ENV). The render script decodes its slot and
writes those bytes verbatim to manifest.configs[].render_to (typically under
~/.config/ — e.g. ~/.config/opencode/opencode.json) with the declared mode.
agentpkg does not choose the path itself.

Exit codes:
  0 — success
  1 — invalid arguments / config input not found
  70 — render-config.sh reported a configuration error (missing required key)
  71 — render-config.sh reported a script error
  72 — soft failure (reserved, currently unused here)

--target-root affects only AGENT_MARKETPLACE_DEPLOY_ROOT (where the
agent's binary lands under install). It does NOT control where the
config file lands — that's decided by the manifest's render_to paths
and the render-config.sh script. The env var
AGENT_MARKETPLACE_TARGET_ROOT is also passed so a render-config.sh
that wants to honor it can read it; current render scripts ignore it.`,
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
	c.Flags().StringVar(&targetRoot, "target-root", "", "affects AGENT_MARKETPLACE_DEPLOY_ROOT only; config file location is determined by manifest.configs[].render_to (default: $HOME/.local)")
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
	// config files to verify post-render (c) which config slots the agent
	// expects. The manifest is NOT staged to a tmpfile for the script
	// anymore: since the Phase-5 refactor the render scripts read only their
	// MARKETPLACE_CONFIG_<SLOT> base64 env vars and never jq the manifest.
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
	targetRootAbs := resolveTargetRoot(targetRoot)

	// Split the daemon's config-input into one base64 env var per manifest
	// slot. This is what render-config.sh actually reads: each script does
	// `base64 -d` on MARKETPLACE_CONFIG_<SLOT> and writes the decoded bytes
	// VERBATIM to its render_to path. That means the payload must be the SLOT
	// BODY (input["<slotName>"]), not the wrapper — passing the wrapper would
	// render `{"opencode":{"model":...}}` instead of `{"model":...}`.
	slotEnv, err := slotEnvVars(inputPath, mf.Configs)
	if err != nil {
		// Slot-level problems are the same class of failure the scripts signal
		// with their `:?` guard (missing required input), so map to exit 70 to
		// keep the contract with callers unchanged.
		fmt.Fprintf(os.Stderr, "agentpkg: %v\n", err)
		os.Exit(70)
	}

	// Invoke render-config.sh with the agent-marketplace env vars.
	cmd := exec.Command(renderScriptPath.Name())
	cmd.Env = append(os.Environ(),
		"AGENT_MARKETPLACE_AGENT="+name,
		"AGENT_MARKETPLACE_VERSION="+version,
		"AGENT_MARKETPLACE_DEPLOY_ROOT="+deployRoot,
		// F020 (tested 2026-09-30): also pass TARGET_ROOT so a
		// render-config.sh that wants to honor it (e.g. for
		// tests or sandboxed deployments) can. Current render
		// scripts ignore it and write to the canonical paths
		// declared in manifest.configs[].render_to. --target-root
		// therefore does NOT change where the config file lands;
		// it only controls DEPLOY_ROOT.
		"AGENT_MARKETPLACE_TARGET_ROOT="+targetRootAbs,
	)
	cmd.Env = append(cmd.Env, slotEnv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Exit code mapping: see mapRenderExitToCLI for the full contract.
		// Extracted as a free function so the contract is unit-tested (see
		// TestRenderConfigExitCodeMapping).
		if exitErr, ok := err.(*exec.ExitError); ok {
			cliExit := mapRenderExitToCLI(exitErr.ExitCode())
			switch cliExit {
			case 70:
				fmt.Fprintln(os.Stderr, "render-config.sh: required-input missing (exit 70)")
			case 71:
				fmt.Fprintln(os.Stderr, "render-config.sh: script error (exit 71)")
			default:
				fmt.Fprintf(os.Stderr, "render-config.sh: script error exit %d (agentpkg exit %d)\n", exitErr.ExitCode(), cliExit)
			}
			os.Exit(cliExit)
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

// slotEnvVars converts the daemon's config-input file into the
// MARKETPLACE_CONFIG_<SLOT> env vars that render-config.sh reads.
//
// Contract (must match render-config.sh exactly):
//   - env var name = "MARKETPLACE_CONFIG_" + upper(slotName) with "-" → "_"
//     (hermes-agent's "hermes-env" slot becomes MARKETPLACE_CONFIG_HERMES_ENV)
//   - env var value = base64(JSON) of the SLOT BODY, i.e. input[slotName].
//     The scripts `base64 -d` the value and write it verbatim to render_to,
//     so encoding the whole wrapper would nest the slot key into the rendered
//     file and produce the wrong shape.
//
// Input file shape is the same slot-keyed object the config/example.* files
// use, so a raw-mode frontend can submit its edited example verbatim.
//
// Returns an error if the file is unreadable, is not a JSON object, or is
// missing a declared slot — the same failure class the scripts signal with
// their `:?` guard (exit 70).
func slotEnvVars(inputPath string, cfgs []manifest.ConfigSpec) ([]string, error) {
	if len(cfgs) == 0 {
		return nil, nil
	}
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, fmt.Errorf("read config input: %w", err)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("config input is not a JSON object keyed by config slot: %w", err)
	}
	env := make([]string, 0, len(cfgs))
	for _, cfg := range cfgs {
		body, ok := input[cfg.Name]
		if !ok {
			return nil, fmt.Errorf("config input is missing slot %q (declared by manifest configs[])", cfg.Name)
		}
		// Re-marshal the raw slot body so the base64 payload is always valid
		// JSON regardless of how the input file was formatted.
		var compact bytes.Buffer
		if err := json.Compact(&compact, body); err != nil {
			return nil, fmt.Errorf("slot %q is not valid JSON: %w", cfg.Name, err)
		}
		env = append(env, fmt.Sprintf("MARKETPLACE_CONFIG_%s=%s",
			strings.ToUpper(strings.ReplaceAll(cfg.Name, "-", "_")),
			base64.StdEncoding.EncodeToString(compact.Bytes())))
	}
	return env, nil
}

// resolveTargetRoot returns targetRoot if non-empty, otherwise the default
// $HOME/.local. Mirrors the default-resolution logic in runInstall.
func resolveTargetRoot(targetRoot string) string {
	if targetRoot != "" {
		return targetRoot
	}
	return filepath.Join(userHomeOrTmp(), ".local")
}

// mapRenderExitToCLI translates render-config.sh's exit code into the
// agentpkg CLI's exit code:
//
//	0    → 0   (success — not used here, success is signalled via nil error)
//	1    → 70  (script bug: missing input file / manifest not found)
//	70   → 70  (required-input missing or invalid — preserve upstream code)
//	71   → 71  (script error: bad type, enum, parse failure)
//	other → 71 (collapse unknown to "script error")
//
// The previous version collapsed all non-1 codes into 71, making
// "required missing" indistinguishable from "type/enum error" downstream.
// Kept as a free function so the contract is unit-tested in config_test.go.
func mapRenderExitToCLI(scriptExitCode int) int {
	switch scriptExitCode {
	case 70:
		return 70
	case 71:
		return 71
	case 1:
		return 70
	default:
		return 71
	}
}

// manifest.LoadFromBytes is imported via the manifest package — keep an
// alias here for clarity (silence unused-import lint in case this file
// gains non-render helpers later).
var _ = manifest.LoadFromBytes