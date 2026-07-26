package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

// installShared are the flag fields shared by install / upgrade / uninstall.
type installShared struct {
	source     string
	channel    string
	version    string
	targetRoot string
	cacheDir   string
	skipRun    bool // for --download-only testing
}

func addInstallFlags(c *cobra.Command, s *installShared) {
	c.Flags().StringVar(&s.source, "source", "upstream", "source tree (upstream | ours)")
	c.Flags().StringVar(&s.channel, "channel", "stable", "channel (stable | beta | dev)")
	c.Flags().StringVar(&s.version, "version", "", "specific version (default: latest stable)")
	c.Flags().StringVar(&s.targetRoot, "target-root", "", "install root (default: $HOME/.local)")
	c.Flags().StringVar(&s.cacheDir, "cache-dir", "", "tarball cache directory (default: $HOME/.local/share/agentpkg/cache)")
	c.Flags().BoolVar(&s.skipRun, "download-only", false, "only download + verify; do not run install.sh")
}

// NewInstallCmd creates `agentpkg install`.
func NewInstallCmd(cfgPath, credsPath *string) *cobra.Command {
	var s installShared
	c := &cobra.Command{
		Use:   "install <agent> [--source upstream] [--channel stable] [--version X.Y.Z]",
		Short: "Download, verify, and run install.sh for an agent",
		Long: `install downloads the tarball (and embedded manifest) for the given
(agent, source, channel, version), verifies sha256, extracts to a temp
directory, then runs install.sh with AGENT_MARKETPLACE_TARGET_ROOT set.

If --version is omitted, the latest stable version of the agent is picked.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			client, err := NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			return runInstall(client, name, &s, false)
		},
	}
	addInstallFlags(c, &s)
	return c
}

// NewUpgradeCmd creates `agentpkg upgrade`.
func NewUpgradeCmd(cfgPath, credsPath *string) *cobra.Command {
	var s installShared
	c := &cobra.Command{
		Use:   "upgrade <agent> [--channel stable] [--version X.Y.Z]",
		Short: "Install a newer version of an already-installed agent",
		Long: `upgrade is currently equivalent to install — install.sh in each tarball
detects the prior version (via state.json) and runs the matching migration
script. If no migration is needed, install.sh simply replaces the files.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			client, err := NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			return runInstall(client, name, &s, true)
		},
	}
	addInstallFlags(c, &s)
	return c
}

// NewUninstallCmd creates `agentpkg uninstall`.
func NewUninstallCmd(cfgPath, credsPath *string) *cobra.Command {
	var s installShared
	c := &cobra.Command{
		Use:   "uninstall <agent> [--source upstream] [--channel stable] [--version X.Y.Z]",
		Short: "Run the agent's uninstall.sh to remove installed files",
		Long: `uninstall reads state.json to find the deployment directory, then runs
the matching uninstall.sh. The tarball is not downloaded; we rely on the
previously installed files being intact.

If --version is omitted, the latest installed version is removed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			_, err := NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			return runUninstall(name, &s)
		},
	}
	c.Flags().StringVar(&s.source, "source", "upstream", "source tree (upstream | ours)")
	c.Flags().StringVar(&s.channel, "channel", "stable", "channel (stable | beta | dev)")
	c.Flags().StringVar(&s.version, "version", "", "specific version (default: latest installed)")
	c.Flags().StringVar(&s.targetRoot, "target-root", "", "install root (default: $HOME/.local)")
	return c
}

// runInstall downloads + verifies + extracts + runs install.sh.
//
// isUpgrade is currently informational only; the install.sh contract handles
// the actual upgrade (it reads state.json and runs the right migration).
func runInstall(c *Client, name string, s *installShared, isUpgrade bool) error {
	version := s.version
	if version == "" {
		v, err := pickLatestStable(c, name, s.source, s.channel)
		if err != nil {
			return fmt.Errorf("resolve latest version: %w", err)
		}
		version = v
		fmt.Printf("Resolved latest %s: %s\n", name, version)
	}

	tarName, expectedSHA, err := resolveTarball(c, name, s.source, s.channel, version)
	if err != nil {
		return err
	}

	cacheDir := s.cacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(userHomeOrTmp(), ".local", "share", "agentpkg", "cache")
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("mkdir cache: %w", err)
	}
	cachePath := filepath.Join(cacheDir, tarName)

	// Skip re-download if cache has a valid copy (still verifies sha256).
	if !needsDownload(cachePath, expectedSHA) {
		fmt.Printf("Using cached %s\n", cachePath)
	} else {
		if err := downloadAndVerify(c, name, s.source, s.channel, version, tarName, expectedSHA, cachePath); err != nil {
			return err
		}
		fmt.Printf("Downloaded %s -> %s\n", tarName, cachePath)
	}

	if s.skipRun {
		fmt.Println("(skipping install.sh because --download-only)")
		return nil
	}

	targetRoot := s.targetRoot
	if targetRoot == "" {
		targetRoot = filepath.Join(userHomeOrTmp(), ".local")
	}
	if err := runInstallScript(cachePath, targetRoot, version, isUpgrade); err != nil {
		return err
	}
	return nil
}

// runUninstall reads state.json and invokes the matching uninstall.sh.
func runUninstall(name string, s *installShared) error {
	targetRoot := s.targetRoot
	if targetRoot == "" {
		targetRoot = filepath.Join(userHomeOrTmp(), ".local")
	}
	stateFile := filepath.Join(targetRoot, "state", name+".state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return fmt.Errorf("read state.json (%s): %w — was this agent installed via agentpkg?", stateFile, err)
	}
	var state struct {
		Version    string `json:"version"`
		Source     string `json:"source"`
		Channel    string `json:"channel"`
		DeployRoot string `json:"deploy_root"`
	}
	if err := jsonUnmarshal(data, &state); err != nil {
		return fmt.Errorf("parse state.json: %w", err)
	}
	if s.version != "" && state.Version != s.version {
		return fmt.Errorf("installed version is %s, --version says %s", state.Version, s.version)
	}

	// Resolve the tarball name and re-download (or use cache) so we have
	// uninstall.sh locally.
	client, err := NewClient("", "") // unused; we'll refetch via separate helper
	if err != nil {
		// We don't actually need a client for uninstall — uninstall.sh lives
		// in the cached tarball. Continue.
	}
	_ = client

	// The cached tarball has uninstall.sh in its root.
	cacheDir := s.cacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(userHomeOrTmp(), ".local", "share", "agentpkg", "cache")
	}
	tarName := fmt.Sprintf("%s-%s-%s.tar.gz", name, state.Source, state.Version)
	tarballPath := filepath.Join(cacheDir, tarName)
	if _, err := os.Stat(tarballPath); err != nil {
		return fmt.Errorf("cached tarball not found at %s — cannot run uninstall.sh without it", tarballPath)
	}

	// Extract uninstall.sh and run it. The tarball may nest the script
	// under the version directory (per `tools/pack.sh`); try that prefix
	// first, then fall back to the bare filename.
	tmp, err := os.MkdirTemp("", "agentpkg-uninstall-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := extractTarballScript(tarballPath, state.Version, tmp); err != nil {
		return fmt.Errorf("extract uninstall.sh: %w", err)
	}
	return execScript(filepath.Join(tmp, "uninstall.sh"),
		[]string{"AGENT_MARKETPLACE_TARGET_ROOT=" + targetRoot},
	)
}

// extractTarballScript pulls uninstall.sh out of a cached tarball. It
// tries "<version>/uninstall.sh" first (the canonical layout produced
// by tools/pack.sh) and falls back to "uninstall.sh" at the root for
// tarballs that don't nest under the version segment. The extracted
// script is always written to destDir/uninstall.sh regardless of its
// archived path.
func extractTarballScript(tarballPath, version, destDir string) error {
	candidates := []string{
		filepath.Join(version, "uninstall.sh"),
		"uninstall.sh",
	}
	for _, name := range candidates {
		if err := runTarExtractTo(tarballPath, name, filepath.Join(destDir, "uninstall.sh")); err == nil {
			return nil
		}
	}
	return fmt.Errorf("uninstall.sh not found (tried: %v)", candidates)
}

// runInstallScript extracts the tarball into a temp dir and runs install.sh.
func runInstallScript(tarballPath, targetRoot, version string, _ bool) error {
	tmp, err := os.MkdirTemp("", "agentpkg-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	// tar -xzf
	cmd := exec.Command("tar", "-xzf", tarballPath, "-C", tmp)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extract tarball: %w", err)
	}
	// The tarball root IS the version directory (agents/<name>/<source>/<version>/).
	// We need to find install.sh inside it. Conventionally it sits at the top
	// level of the tarball root — which is the version segment.
	installSh := filepath.Join(tmp, version, "install.sh")
	if _, err := os.Stat(installSh); err != nil {
		// Try without the version segment (some packers don't include it).
		installSh = filepath.Join(tmp, "install.sh")
		if _, err2 := os.Stat(installSh); err2 != nil {
			return fmt.Errorf("install.sh not found inside tarball")
		}
	}
	fmt.Printf("Running install.sh (target=%s)...\n", targetRoot)
	return execScript(installSh, []string{"AGENT_MARKETPLACE_TARGET_ROOT=" + targetRoot})
}

func execScript(path string, env []string) error {
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// pickLatestStable queries the index for the highest-version stable entry.
func pickLatestStable(c *Client, name, source, channel string) (string, error) {
	var resp struct {
		Agents []struct {
			Name     string `json:"name"`
			Versions []struct {
				Version string `json:"version"`
				Channel string `json:"channel"`
				Source  string `json:"source"`
			} `json:"versions"`
		} `json:"agents"`
	}
	if err := c.do("/api/v1/index", &resp); err != nil {
		return "", err
	}
	for _, a := range resp.Agents {
		if a.Name != name {
			continue
		}
		var best string
		for _, v := range a.Versions {
			if v.Source != source || v.Channel != channel {
				continue
			}
			if v.Version > best {
				best = v.Version
			}
		}
		if best != "" {
			return best, nil
		}
	}
	return "", fmt.Errorf("no %s/%s/%s/* found", name, source, channel)
}

// needsDownload returns true if cachePath doesn't exist or its sha256
// doesn't match expected.
func needsDownload(cachePath, expectedSHA string) bool {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return true
	}
	h := sha256New()
	h.Write(data)
	actual := hexEncode(h.Sum(nil))
	expected := trimSHA256Prefix(expectedSHA)
	return actual != expected
}

// extractSingleFile extracts a single named file from a tar.gz to destDir.
// Used to fish out uninstall.sh from a cached tarball.
func extractSingleFile(tarballPath, fileName, destDir string) error {
	return runTarExtract(tarballPath, fileName, destDir)
}

// userHomeOrTmp returns $HOME, falling back to /tmp if empty.
func userHomeOrTmp() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/tmp"
}
