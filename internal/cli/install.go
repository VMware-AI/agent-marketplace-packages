package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/manifest"
	"github.com/spf13/cobra"
)

// installShared are the flag fields shared by install / upgrade / uninstall.
type installShared struct {
	source      string
	channel     string
	version     string
	targetRoot  string
	cacheDir    string
	skipRun     bool // for --download-only testing
	noServices  bool // skip systemd unit generation + start (manual control)
}

func addInstallFlags(c *cobra.Command, s *installShared) {
	c.Flags().StringVar(&s.source, "source", "upstream", "source tree (upstream | ours)")
	c.Flags().StringVar(&s.channel, "channel", "stable", "channel (stable | beta | dev)")
	c.Flags().StringVar(&s.version, "version", "", "specific version (default: latest stable)")
	c.Flags().StringVar(&s.targetRoot, "target-root", "", "install root (default: $HOME/.local)")
	c.Flags().StringVar(&s.cacheDir, "cache-dir", "", "tarball cache directory (default: $HOME/.local/share/agentpkg/cache)")
	c.Flags().BoolVar(&s.skipRun, "download-only", false, "only download + verify; do not run install.sh")
	c.Flags().BoolVar(&s.noServices, "no-services", false, "skip systemd unit generation + start; for manual control")
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
//
// After install.sh, runInstall reads the manifest's services[] and writes
// a systemd --user unit per service, then enables it. State.json is then
// augmented with services/configs/config_dir fields for uninstall-time
// bookkeeping. Pass --no-services to skip the systemd step.
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

	// Post-install: read manifest, write systemd units, augment state.json.
	if err := postInstall(name, version, targetRoot, cachePath, s.noServices); err != nil {
		// Soft-warn instead of failing the install — services not starting
		// shouldn't block the install from succeeding (state.json updated,
		// next install will retry). Print the warning.
		fmt.Fprintf(os.Stderr, "WARN: post-install steps failed: %v\n", err)
	}
	return nil
}

// postInstall reads the manifest from the cached tarball, writes systemd
// --user units for each service (unless noServices is true), and augments
// state.json with services/configs/config_dir fields.
func postInstall(name, version, targetRoot, tarballPath string, noServices bool) error {
	mf, err := extractManifestFromTarball(tarballPath, version)
	if err != nil {
		return fmt.Errorf("extract manifest post-install: %w", err)
	}

	// Compute deploy_root (matches install.sh's logic).
	deployRoot := filepath.Join(targetRoot, name, version)

	// Build the list of systemd units we created (used by state.json + uninstall).
	var createdServices []stateService
	if !noServices && len(mf.Services) > 0 {
		for _, svc := range mf.Services {
			unit := serviceSpecToUnitFile(name, svc, deployRoot)
			unitPath, err := writeSystemdUserUnit(name, svc, unit)
			if err != nil {
				return fmt.Errorf("write %s unit: %w", svc.Name, err)
			}
			started := enableSystemdUserUnit(unitPath)
			createdServices = append(createdServices, stateService{
				Name:     svc.Name,
				UnitPath: unitPath,
				Started:  started,
			})
		}
		// Daemon-reload once after all units written.
		if err := runSystemctlUser("daemon-reload"); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: systemctl --user daemon-reload: %v\n", err)
		}
	}

	// Augment state.json.
	if err := appendStateJsonFields(targetRoot, name, deployRoot, mf, createdServices); err != nil {
		return fmt.Errorf("update state.json: %w", err)
	}
	return nil
}

// stateService mirrors the per-service shape we append to state.json.
type stateService struct {
	Name     string `json:"name"`
	UnitPath string `json:"unit_path"`
	Started  bool   `json:"started"`
}

type stateConfig struct {
	Name     string `json:"name"`
	RenderTo string `json:"render_to"`
	Mode     string `json:"mode"`
}

// appendStateJsonFields reads the agent's state.json (written by install.sh)
// and appends services[], configs[], config_dir. Idempotent: re-running
// replaces these fields instead of duplicating.
func appendStateJsonFields(targetRoot, name, deployRoot string, mf *manifest.Manifest, services []stateService) error {
	stateFile := filepath.Join(targetRoot, "state", name+".state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return fmt.Errorf("read state file: %w", err)
	}
	// Decode into a generic map so we don't break existing fields.
	var state map[string]any
	if err := jsonUnmarshal(data, &state); err != nil {
		return fmt.Errorf("parse state file: %w", err)
	}

	// Build the new fields.
	svcs := make([]stateService, 0, len(services))
	for _, s := range services {
		svcs = append(svcs, s)
	}
	if len(svcs) == 0 {
		// Preserve any pre-existing services[] in state.json (e.g. from a
		// prior install). Don't clobber when --no-services was passed.
		if existing, ok := state["services"].([]any); ok {
			for _, e := range existing {
				if m, ok := e.(map[string]any); ok {
					svcs = append(svcs, stateService{
						Name:     asString(m["name"]),
						UnitPath: asString(m["unit_path"]),
						Started:  asBool(m["started"]),
					})
				}
			}
		}
	}

	cfgs := make([]stateConfig, 0, len(mf.Configs))
	for _, c := range mf.Configs {
		cfgs = append(cfgs, stateConfig{Name: c.Name, RenderTo: c.RenderTo, Mode: c.Mode})
	}
	if len(cfgs) == 0 {
		if existing, ok := state["configs"].([]any); ok {
			for _, e := range existing {
				if m, ok := e.(map[string]any); ok {
					cfgs = append(cfgs, stateConfig{
						Name:     asString(m["name"]),
						RenderTo: asString(m["render_to"]),
						Mode:     asString(m["mode"]),
					})
				}
			}
		}
	}

	state["services"] = svcs
	state["configs"] = cfgs
	state["config_dir"] = filepath.Join(deployRoot, "config")

	out, err := jsonMarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := os.WriteFile(stateFile, out, 0644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// serviceSpecToUnitFile generates a systemd --user unit file body from a
// manifest.ServiceSpec. Substitutes {{DEPLOY_ROOT}} with the absolute
// deploy_root path. If the first token of `command` resolves to a binary
// at `$deploy_root/bin/<token>`, that absolute path is used instead —
// systemd --user has a minimal PATH and won't find relative binaries.
func serviceSpecToUnitFile(agent string, svc manifest.ServiceSpec, deployRoot string) string {
	workingDir := svc.WorkingDir
	if workingDir == "" {
		workingDir = deployRoot
	}
	workingDir = substituteVars(workingDir, map[string]string{"DEPLOY_ROOT": deployRoot})

	command := substituteSliceVars(svc.Command, map[string]string{"DEPLOY_ROOT": deployRoot})

	// Resolve the first token to an absolute path if it's a relative binary
	// that exists at $deployRoot/bin/<token>. This is the conventional layout
	// after install.sh copies the agent's payload there.
	if len(command) > 0 && !filepath.IsAbs(command[0]) {
		candidate := filepath.Join(deployRoot, "bin", command[0])
		if _, err := os.Stat(candidate); err == nil {
			command[0] = candidate
		}
	}

	var args strings.Builder
	args.WriteString("[Unit]\n")
	args.WriteString(fmt.Sprintf("Description=%s\n", svc.Description))
	args.WriteString(fmt.Sprintf("After=network.target\n\n"))
	args.WriteString("[Service]\n")
	args.WriteString("Type=simple\n")
	args.WriteString(fmt.Sprintf("WorkingDirectory=%s\n", workingDir))
	if len(command) > 0 {
		args.WriteString(fmt.Sprintf("ExecStart=%s\n", strings.Join(command, " ")))
	}
	if svc.Restart != "" {
		args.WriteString(fmt.Sprintf("Restart=%s\n", svc.Restart))
	}
	args.WriteString("\n[Install]\n")
	args.WriteString("WantedBy=default.target\n")
	return args.String()
}

func substituteVars(s string, vars map[string]string) string {
	out := s
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

func substituteSliceVars(in []string, vars map[string]string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = substituteVars(s, vars)
	}
	return out
}

// writeSystemdUserUnit writes a unit file to ~/.config/systemd/user/
// and returns the path it was written to.
func writeSystemdUserUnit(agent string, svc manifest.ServiceSpec, body string) (string, error) {
	dir := filepath.Join(userHomeOrTmp(), ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	unitName := fmt.Sprintf("%s-%s.service", agent, svc.Name)
	unitPath := filepath.Join(dir, unitName)
	if err := os.WriteFile(unitPath, []byte(body), 0644); err != nil {
		return "", err
	}
	return unitPath, nil
}

// enableSystemdUserUnit runs `systemctl --user enable --now <unit>` then
// polls `is-active` to confirm the unit actually started. Returns true if
// the unit is active. Returns false on soft failure (systemctl --user
// not available, unit failed to start, etc.).
func enableSystemdUserUnit(unitPath string) bool {
	unitName := filepath.Base(unitPath)
	if err := exec.Command("systemctl", "--user", "enable", "--now", unitName).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: systemctl --user enable --now %s: %v\n", unitName, err)
		return false
	}
	// enable --now returns 0 even if the service crashes on first start.
	// Poll is-active briefly to confirm the unit actually came up.
	for i := 0; i < 5; i++ {
		if err := exec.Command("systemctl", "--user", "is-active", "--quiet", unitName).Run(); err == nil {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "WARN: %s did not become active after enable --now\n", unitName)
	return false
}

func runSystemctlUser(args ...string) error {
	fullArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", fullArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (output: %s)", err, string(out))
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
		Version    string        `json:"version"`
		Source     string        `json:"source"`
		Channel    string        `json:"channel"`
		DeployRoot string        `json:"deploy_root"`
		Services   []stateService `json:"services"`
		Configs    []stateConfig  `json:"configs"`
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

	// Stop + remove systemd --user units before running uninstall.sh.
	// uninstall.sh may also remove deploy_root; doing units first avoids
	// dangling WorkingDirectory= references.
	for _, svc := range state.Services {
		if svc.UnitPath == "" {
			continue
		}
		if err := disableSystemdUserUnit(svc.UnitPath); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: disable %s: %v\n", svc.UnitPath, err)
		}
		if err := os.Remove(svc.UnitPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "WARN: rm %s: %v\n", svc.UnitPath, err)
		}
	}
	if len(state.Services) > 0 {
		if err := runSystemctlUser("daemon-reload"); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: daemon-reload: %v\n", err)
		}
	}

	// Best-effort removal of rendered config files (uninstall.sh may
	// have already cleaned them via DEPLOY_ROOT removal).
	for _, cfg := range state.Configs {
		if cfg.RenderTo == "" {
			continue
		}
		// Expand ~ to $HOME (config.RenderTo is verbatim from manifest).
		path := cfg.RenderTo
		if strings.HasPrefix(path, "~/") {
			path = filepath.Join(userHomeOrTmp(), path[2:])
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "WARN: rm config %s: %v\n", path, err)
		}
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

// disableSystemdUserUnit runs `systemctl --user disable --now <unit>`.
// Best-effort: soft-fail if systemctl --user is unavailable.
func disableSystemdUserUnit(unitPath string) error {
	unitName := filepath.Base(unitPath)
	out, err := exec.Command("systemctl", "--user", "disable", "--now", unitName).CombinedOutput()
	if err != nil {
		// Soft-fail: not fatal.
		return fmt.Errorf("%w (output: %s)", err, string(out))
	}
	return nil
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

// extractManifestFromTarball pulls manifest.json out of a cached tarball,
// mirroring extractTarballScript. Tries "<version>/manifest.json" first
// (the canonical pack.sh layout) then "manifest.json" at the root. The
// extracted JSON is then parsed via manifest.LoadFromBytes.
func extractManifestFromTarball(tarballPath, version string) (*manifest.Manifest, error) {
	tmp, err := os.MkdirTemp("", "agentpkg-mf-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	candidates := []string{
		filepath.Join(version, "manifest.json"),
		"manifest.json",
	}
	for _, name := range candidates {
		out := filepath.Join(tmp, "manifest.json")
		if err := runTarExtractTo(tarballPath, name, out); err == nil {
			data, err := os.ReadFile(out)
			if err != nil {
				return nil, fmt.Errorf("read extracted manifest: %w", err)
			}
			return manifest.LoadFromBytes(data)
		}
	}
	return nil, fmt.Errorf("manifest.json not found in tarball (tried: %v)", candidates)
}

// extractRenderScriptFromTarball pulls render-config.sh out of a cached
// tarball. Same two-layout fallback as extractTarballScript. The
// extracted file is preserved at executable mode from the tarball.
func extractRenderScriptFromTarball(tarballPath, version, destPath string) error {
	candidates := []string{
		filepath.Join(version, "render-config.sh"),
		"render-config.sh",
	}
	for _, name := range candidates {
		if err := runTarExtractTo(tarballPath, name, destPath); err == nil {
			// Ensure executable bit. tools/pack.sh preserves modes, but
			// some host tar implementations may strip them. Defensive chmod.
			if err := os.Chmod(destPath, 0755); err != nil {
				return fmt.Errorf("chmod render-config.sh: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("render-config.sh not found in tarball (tried: %v)", candidates)
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
