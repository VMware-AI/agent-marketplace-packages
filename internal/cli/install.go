package cli

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
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
	dryRun      bool // upgrade only: print the planned action without downloading or executing
}

func addInstallFlags(c *cobra.Command, s *installShared) {
	c.Flags().StringVar(&s.source, "source", "upstream", "source tree (upstream | ours)")
	c.Flags().StringVar(&s.channel, "channel", "stable", "channel (stable | beta | dev)")
	c.Flags().StringVar(&s.version, "version", "", "specific version (default: latest stable)")
	c.Flags().StringVar(&s.targetRoot, "target-root", "", "install root (default: $HOME/.local)")
	c.Flags().StringVar(&s.cacheDir, "cache-dir", "", "tarball cache directory (default: $HOME/.local/share/agentpkg/cache)")
	c.Flags().BoolVar(&s.skipRun, "download-only", false, "only download + verify; do not run install.sh")
	c.Flags().BoolVar(&s.noServices, "no-services", false, "skip systemd unit generation + start; for manual control")
	c.Flags().BoolVar(&s.dryRun, "dry-run", false, "(upgrade only) print the planned action without downloading or executing; exit 0")
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

// NewRollbackCmd creates `agentpkg rollback`.
//
// rollback reads state.json's "previous" block (schema 1.1+) to find the
// version that was installed before the most recent upgrade, then runs the
// matching inverse migration (migrate/to-<from>.sh) if the target tarball
// provides one, and finally runs the target version's install.sh — which
// itself performs the file-level replacement. Use --to-version / --to-source
// to roll back further than just one step.
//
// Failure modes (mapped to distinct exit codes so an orchestrator can
// surface them; see internal/cli/exitcodes.go):
//
//   - ExitRollbackNoTarget      (74): state.json missing or has no
//     "previous" block
//   - ExitRollbackTargetUnknown (75): target version not in the index
//     or tarball not resolvable
//   - ExitRollbackDownloadFail  (76): tarball download or sha256
//     verify failure
//
// rollback does NOT prompt, does NOT read stdin, and never blocks.
func NewRollbackCmd(cfgPath, credsPath *string) *cobra.Command {
	var s installShared
	var (
		toVersion string
		toSource  string
		jsonOut   bool
	)
	c := &cobra.Command{
		Use:   "rollback <agent> [--to-version X.Y.Z] [--to-source upstream] [--json]",
		Short: "Revert an installed agent to its previously-recorded version",
		Long: `rollback reads state.json's "previous" block to find the version that
was installed before the most recent upgrade, then runs the matching inverse
migration (migrate/to-<from>.sh) if the target tarball provides one, and
finally runs the target version's install.sh — which itself performs the
file-level replacement.

Use --to-version / --to-source to roll back further than just one step.

If no inverse migration is shipped for the target version, agentpkg proceeds
without one (matches the manual fallback in docs/upgrade-protocol.md).

If state.json is missing or has no "previous" block, rollback exits 74 so
an orchestrator can surface the error instead of silently doing nothing.

--json prints the resolved rollback plan as a single JSON object to
stdout and exits 0 without downloading, extracting, or running any
script. Output schema: see printRollbackPlanJSON / docs/agentpkg.md.
Intended for orchestrators that want a machine-readable preview before
deciding to invoke the real rollback.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			// --json is a pure read of state.json. Skip the HTTP client
			// round-trip — we don't touch the marketplace API in this path.
			if jsonOut {
				return planRollbackJSON(name, &s, toVersion, toSource)
			}
			client, err := NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			return runRollback(client, name, &s, toVersion, toSource)
		},
	}
	c.Flags().StringVar(&s.source, "source", "", "source tree override (default: read from state.json.previous.source)")
	c.Flags().StringVar(&s.channel, "channel", "stable", "channel (stable | beta | dev)")
	c.Flags().StringVar(&s.targetRoot, "target-root", "", "install root (default: $HOME/.local)")
	c.Flags().StringVar(&s.cacheDir, "cache-dir", "", "tarball cache directory (default: $HOME/.local/share/agentpkg/cache)")
	c.Flags().BoolVar(&s.noServices, "no-services", false, "skip systemd unit generation + start; for manual control")
	c.Flags().StringVar(&toVersion, "to-version", "", "override the rollback target version (default: state.json.previous.version)")
	c.Flags().StringVar(&toSource, "to-source", "", "override the rollback target source tree (default: state.json.previous.source)")
	c.Flags().BoolVar(&jsonOut, "json", false, "print the rollback plan as JSON to stdout and exit 0 (no download, no execution)")
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

	// --dry-run short-circuit. Only meaningful for upgrade (install has
	// nothing to preview). Print the resolved plan and exit 0 without
	// touching install.sh, systemd units, or state.json. Does NOT inspect
	// the tarball for migration scripts — that requires extraction.
	if isUpgrade && s.dryRun {
		targetRoot := s.targetRoot
		if targetRoot == "" {
			targetRoot = filepath.Join(userHomeOrTmp(), ".local")
		}
		printUpgradePlan(name, version, s, targetRoot, cachePath, expectedSHA)
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
// at one of the conventional deploy_root subpaths, that absolute path is
// used instead — systemd --user has a minimal PATH and won't find relative
// binaries. We probe a small list of candidate locations matching how
// install.sh places binaries for the three known agents.
//
// Order of probe (first match wins):
//   1. {{DEPLOY_ROOT}}/bin/<token>          — opencode, openclaw
//   2. {{DEPLOY_ROOT}}/venv/bin/<token>      — hermes-agent (Python venv layout)
//   3. As-given in the manifest              — fallback for paths the manifest already provides
func serviceSpecToUnitFile(agent string, svc manifest.ServiceSpec, deployRoot string) string {
	workingDir := svc.WorkingDir
	if workingDir == "" {
		workingDir = deployRoot
	}
	workingDir = substituteVars(workingDir, map[string]string{"DEPLOY_ROOT": deployRoot})

	command := substituteSliceVars(svc.Command, map[string]string{"DEPLOY_ROOT": deployRoot})

	if len(command) > 0 && !filepath.IsAbs(command[0]) {
		candidates := []string{
			filepath.Join(deployRoot, "bin", command[0]),
			filepath.Join(deployRoot, "venv", "bin", command[0]),
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				command[0] = candidate
				break
			}
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

// rollbackError lets runRollback signal a specific exit code without
// leaking exit-code constants into the call chain. The caller (main.go)
// reads the embedded code and translates it to os.Exit. Anywhere else
// can compare to a sentinel with errors.As.
//
// The wrapped error (when set) is preserved via Unwrap so callers can
// errors.Is/As against the inner cause. We don't use %w inside Sprintf
// because newRollbackErr composes a fixed format string with an opaque
// error argument.
type rollbackError struct {
	code int
	msg  string
	err  error
}

func (e *rollbackError) Error() string { return e.msg }

func (e *rollbackError) Unwrap() error { return e.err }

func (e *rollbackError) ExitCode() int { return e.code }

// wrapRollbackErr attaches a code to a wrapped error. err may be nil;
// a non-nil inner error is exposed via Unwrap.
func wrapRollbackErr(code int, err error, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if err != nil {
		msg = msg + ": " + err.Error()
	}
	return &rollbackError{code: code, msg: msg, err: err}
}

// readStatePrevious reads the agent's state.json and returns
// (currentVersion, targetVersion, targetSource, targetChannel, err).
// currentVersion is the "version" field of the live state.json (used to
// name the inverse migration script: migrate/to-<currentVersion>.sh).
// targetVersion / targetSource come from .previous; targetChannel also
// comes from .previous when present (schema 1.2+), falling back to ""
// for older state.json files — callers must default the channel
// themselves. Returns (..., ExitRollbackNoTarget) when state.json is
// missing or has no usable .previous block.
//
// The channel field is optional so that agents whose install.sh has not
// been updated to write it still roll back cleanly — when channel is "",
// `agentpkg rollback` defaults to "stable" with a stderr note.
func readStatePrevious(name, targetRoot string) (currentVersion, targetVersion, targetSource, targetChannel string, err error) {
	data, err := os.ReadFile(filepath.Join(targetRoot, "state", name+".state.json"))
	if err != nil {
		return "", "", "", "", wrapRollbackErr(ExitRollbackNoTarget, err,
			"read state.json (%s) — was this agent installed via agentpkg?",
			filepath.Join(targetRoot, "state", name+".state.json"))
	}
	var state map[string]any
	if err := jsonUnmarshal(data, &state); err != nil {
		return "", "", "", "", wrapRollbackErr(ExitRollbackNoTarget, err, "parse state.json")
	}
	currentVersion = asString(state["version"])
	prev, ok := state["previous"].(map[string]any)
	if !ok || prev == nil {
		return "", "", "", "", wrapRollbackErr(ExitRollbackNoTarget, nil,
			"state.json has no .previous block — cannot determine rollback target (pre-1.1 install? fresh install?)")
	}
	targetVersion = asString(prev["version"])
	targetSource = asString(prev["source"])
	targetChannel = asString(prev["channel"])
	if targetVersion == "" {
		return "", "", "", "", wrapRollbackErr(ExitRollbackNoTarget, nil,
			"state.json .previous.version is empty — no rollback target recorded")
	}
	return currentVersion, targetVersion, targetSource, targetChannel, nil
}

// hasMigrateToScript reports whether the target-version tarball ships
// a migrate/to-<fromVersion>.sh inverse migration. Pure presence check —
// does NOT execute. Tries both layouts (per docs/upgrade-protocol.md):
//   <targetVersion>/migrate/to-<fromVersion>.sh   (canonical, tools/pack.sh)
//   migrate/to-<fromVersion>.sh                  (root fallback)
// Rejects false positives where a payload/ entry happens to share the
// basename.
func hasMigrateToScript(tarballPath, targetVersion, fromVersion string) bool {
	_, matched := resolveInverseMigration(tarballPath, targetVersion, fromVersion)
	return matched
}

// resolveInverseMigration picks the inverse-migration script from the
// target tarball using the same priority scheme that install.sh uses
// for forward migrations (docs/upgrade-protocol.md:39-42):
//
//  1. exact match: to-<from>.sh
//  2. major.minor wildcard: to-<major>.<minor>.x.sh
//  3. major wildcard: to-<major>.x.x.sh
//
// Returns the chosen script's basename (always "to-<...>.sh") and
// matched=true; or "" / matched=false when none exist. Both layouts
// (nested under <targetVersion>/migrate/ and root migrate/) are tried
// per candidate.
//
// We do this in agentpkg rather than in install.sh so the rollback
// flow has a single owner for migration-script selection; install.sh
// does NOT participate on the rollback path (it only writes the new
// state.json after agentpkg has already swapped versions).
func resolveInverseMigration(tarballPath, targetVersion, fromVersion string) (basename string, matched bool) {
	candidates := inverseMigrationCandidates(fromVersion)
	for _, c := range candidates {
		if archiveHasMigrateTo(tarballPath, filepath.Join(targetVersion, "migrate", c)) {
			return c, true
		}
		if archiveHasMigrateTo(tarballPath, filepath.Join("migrate", c)) {
			return c, true
		}
	}
	return "", false
}

// inverseMigrationCandidates enumerates the basenames the rollback flow
// will consider for a given fromVersion, in priority order (exact →
// major.minor wildcard → major wildcard).
func inverseMigrationCandidates(fromVersion string) []string {
	if fromVersion == "" {
		return nil
	}
	cands := []string{"to-" + fromVersion + ".sh"}
	if major, minor, ok := splitSemver(fromVersion); ok {
		cands = append(cands,
			"to-"+major+"."+minor+".x.sh",
			"to-"+major+".x.x.sh",
		)
	}
	return cands
}

// splitSemver parses "MAJOR.MINOR.PATCH[.suffix...]" into (major, minor,
// ok). For inverse-migration script matching we only care about the
// first two dotted components; the suffix (e.g. "-rc1") is ignored.
func splitSemver(v string) (major, minor string, ok bool) {
	first := strings.SplitN(v, ".", 3)
	if len(first) < 2 {
		return "", "", false
	}
	if first[0] == "" || first[1] == "" {
		return "", "", false
	}
	return first[0], strings.SplitN(first[1], ".", 2)[0], true
}

// archiveHasMigrateTo returns true if the tarball has a path whose dir
// is "migrate/" and whose basename is "to-<fromVersion>.sh". This is
// stricter than runTarContains (basename-only) and protects against
// false positives from unrelated tarball entries.
func archiveHasMigrateTo(tarballPath, wantPath string) bool {
	f, err := os.Open(tarballPath)
	if err != nil {
		return false
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return false
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			return false
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Name == wantPath {
			return true
		}
		// Also accept any "<prefix>/migrate/to-<X>.sh" form.
		dir, base := filepath.Split(hdr.Name)
		if filepath.Base(filepath.Clean(dir)) == "migrate" && base == "to-"+filepath.Base(wantPath) {
			return true
		}
	}
}

// extractMigrateToScript pulls the inverse-migration script matched by
// resolveInverseMigration out of a cached tarball. The script is
// extracted to destDir using a fixed local filename (to-<from>.sh) so
// execScript's path is stable regardless of which wildcard matched.
// Made executable before returning.
func extractMigrateToScript(tarballPath, targetVersion, fromVersion, destDir string) (string, error) {
	basename, ok := resolveInverseMigration(tarballPath, targetVersion, fromVersion)
	if !ok {
		return "", fmt.Errorf("no inverse migration found for from=%s in tarball", fromVersion)
	}
	candidates := []string{
		filepath.Join(targetVersion, "migrate", basename),
		filepath.Join("migrate", basename),
	}
	out := filepath.Join(destDir, basename)
	for _, name := range candidates {
		if err := runTarExtractTo(tarballPath, name, out); err == nil {
			if err := os.Chmod(out, 0755); err != nil {
				return "", fmt.Errorf("chmod: %w", err)
			}
			return out, nil
		}
	}
	return "", fmt.Errorf("to-%s.sh resolved but extraction failed (tried: %v)", fromVersion, candidates)
}

// runInverseMigration runs the optional inverse migration script from
// the cached target tarball. The script basename is resolved via the
// same exact → major.minor wildcard → major wildcard priority that
// install.sh uses for forward migrations
// (docs/upgrade-protocol.md:39-42). On any failure prints WARN and
// returns nil (mirrors the from-<old>.sh contract — non-fatal per
// docs/upgrade-protocol.md:52). Returns nil when the script is absent.
func runInverseMigration(tarballPath, targetVersion, fromVersion, targetRoot, currentDeployRoot string) error {
	basename, ok := resolveInverseMigration(tarballPath, targetVersion, fromVersion)
	if !ok {
		fmt.Fprintf(os.Stderr, "WARN: no migrate/to-*.sh matching from=%s in target tarball — skipping inverse migration\n", fromVersion)
		return nil
	}
	tmp, err := os.MkdirTemp("", "agentpkg-rollback-mig-")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmp)
	out, err := extractMigrateToScript(tarballPath, targetVersion, fromVersion, tmp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: extract %s: %v\n", basename, err)
		return nil
	}
	fmt.Printf("Running inverse migration %s...\n", basename)
	env := []string{
		"AGENT_MARKETPLACE_TARGET_ROOT=" + targetRoot,
		"AGENT_MARKETPLACE_TARGET_VERSION=" + targetVersion,
		"AGENT_MARKETPLACE_DEPLOY_ROOT=" + currentDeployRoot,
	}
	if err := execScript(out, env); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: inverse migration %s failed: %v — continuing with install.sh\n", out, err)
		return nil
	}
	return nil
}

// runRollback is the driver behind `agentpkg rollback`. See
// NewRollbackCmd for the contract.
func runRollback(c *Client, name string, s *installShared, toVersion, toSource string) error {
	targetRoot := s.targetRoot
	if targetRoot == "" {
		targetRoot = filepath.Join(userHomeOrTmp(), ".local")
	}

	// 1. Read state.json and resolve target.
	currentVersion, targetV, targetSrc, targetCh, err := readStatePrevious(name, targetRoot)
	if err != nil {
		return err
	}
	if toVersion != "" {
		targetV = toVersion
	}
	if toSource != "" {
		targetSrc = toSource
	}
	if targetV == "" || targetSrc == "" {
		return wrapRollbackErr(ExitRollbackNoTarget, nil,
			"rollback target not fully resolved: version=%q source=%q", targetV, targetSrc)
	}

	// 2. Default --source / --channel if not set on CLI. Channel comes
	//    from state.json.previous when the install.sh that wrote it was
	//    new enough (schema 1.2+); older state.json files leave it ""
	//    and we fall back to "stable" with a one-line note so an
	//    operator who rolled back an older agent can spot it.
	if s.source == "" {
		s.source = targetSrc
	}
	if s.channel == "" {
		if targetCh != "" {
			s.channel = targetCh
		} else {
			fmt.Fprintf(os.Stderr, "WARN: state.json has no .previous.channel — defaulting to stable (pass --channel to override)\n")
			s.channel = "stable"
		}
	}

	// 3. Resolve the target tarball.
	tarName, expectedSHA, err := resolveTarball(c, name, s.source, s.channel, targetV)
	if err != nil {
		return wrapRollbackErr(ExitRollbackTargetUnknown, err,
			"resolve target tarball %s/%s/%s/%s",
			name, s.source, s.channel, targetV)
	}

	// 4. Cache / download the target tarball.
	cacheDir := s.cacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(userHomeOrTmp(), ".local", "share", "agentpkg", "cache")
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return wrapRollbackErr(ExitRollbackDownloadFail, err, "mkdir cache")
	}
	cachePath := filepath.Join(cacheDir, tarName)
	if !needsDownload(cachePath, expectedSHA) {
		fmt.Printf("Using cached %s\n", cachePath)
	} else {
		if err := downloadAndVerify(c, name, s.source, s.channel, targetV, tarName, expectedSHA, cachePath); err != nil {
			return wrapRollbackErr(ExitRollbackDownloadFail, err, "download")
		}
		fmt.Printf("Downloaded %s -> %s\n", tarName, cachePath)
	}

	// 5. Optional inverse migration. The "from" version is the one we
	//    are rolling back FROM (currentVersion); the "target" version
	//    is the one we're installing. The script is looked up inside the
	//    target tarball at <targetVersion>/migrate/to-<fromVersion>.sh.
	//    If currentVersion is empty (e.g. never had one recorded),
	//    skip this step entirely.
	if currentVersion != "" {
		currentDeployRoot := filepath.Join(targetRoot, name, currentVersion)
		if err := runInverseMigration(cachePath, targetV, currentVersion, targetRoot, currentDeployRoot); err != nil {
			// runInverseMigration already prints WARN; we only get an
			// error here for mktemp failures etc. — bubble it up.
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "WARN: no current version recorded in state.json — skipping inverse migration\n")
	}

	// 6. Run target install.sh. This is the actual file replacement; it
	//    also rewrites state.json with the new version and a fresh
	//    .previous pointing at currentVersion (so a subsequent rollback
	//    can come back here).
	if err := runInstallScript(cachePath, targetRoot, targetV, true); err != nil {
		return fmt.Errorf("install.sh (rollback target %s): %w", targetV, err)
	}

	// 7. Post-install. Soft-warn on failure (same policy as runInstall).
	if err := postInstall(name, targetV, targetRoot, cachePath, s.noServices); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: post-install steps failed: %v\n", err)
	}

	from := currentVersion
	if from == "" {
		from = "(none)"
	}
	fmt.Printf("Rolled back %s: %s -> %s\n", name, from, targetV)
	return nil
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

// extractManifestBytesFromTarball extracts manifest.json from a tarball
// and returns its raw bytes — without parsing. Useful for staging the
// manifest to a tmpfile so render-config.sh can read it directly via
// $AGENT_MARKETPLACE_MANIFEST. Same two-layout fallback as
// extractManifestFromTarball.
func extractManifestBytesFromTarball(tarballPath, version string) ([]byte, error) {
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
			return data, nil
		}
	}
	return nil, fmt.Errorf("manifest.json not found in tarball (tried: %v)", candidates)
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

// printUpgradePlan writes the human-readable upgrade plan to stdout.
// Called from runInstall only when --dry-run + isUpgrade. Output is the
// single source of truth that orchestrators (or a human operator) read
// before deciding to re-run without --dry-run.
func printUpgradePlan(name, version string, s *installShared, targetRoot, cachePath, expectedSHA string) {
	current := readInstalledVersion(name, targetRoot)
	steps := []string{
		"[1] extract tarball to temp dir, locate install.sh",
		"[2] install.sh runs (it reads state.json and decides whether to run a migration script)",
		"[3] write/update systemd --user units (skipped with --no-services)",
		"[4] append services/configs/config_dir to state.json",
	}
	fmt.Printf("(dry-run) Upgrade plan for: %s\n", name)
	if current != "" {
		fmt.Printf("  current:        %s (per state.json)\n", current)
	} else {
		fmt.Printf("  current:        (no prior install detected — install.sh will treat this as a fresh install)\n")
	}
	fmt.Printf("  target:         %s\n", version)
	fmt.Printf("  source:         %s\n", s.source)
	fmt.Printf("  channel:        %s\n", s.channel)
	fmt.Printf("  target root:    %s\n", targetRoot)
	fmt.Printf("  cache:          %s\n", cachePath)
	fmt.Printf("  expected sha:   %s\n", expectedSHA)
	fmt.Printf("  steps:\n")
	for _, step := range steps {
		fmt.Printf("    %s\n", step)
	}
	fmt.Printf("  note: --dry-run does NOT inspect the tarball for migration scripts.\n")
	fmt.Printf("        re-run without --dry-run to actually execute.\n")
}

// readInstalledVersion returns the version field from the agent's
// state.json, or "" if state.json is missing/unparseable. Used by
// --dry-run to surface "what's currently there" in the plan. Does not
// error — a missing state.json means "fresh install", which the plan
// already handles.
func readInstalledVersion(name, targetRoot string) string {
	data, err := os.ReadFile(filepath.Join(targetRoot, "state", name+".state.json"))
	if err != nil {
		return ""
	}
	var state map[string]any
	if err := jsonUnmarshal(data, &state); err != nil {
		return ""
	}
	return asString(state["version"])
}

// readStateTopLevel returns the value of a top-level string field from
// the agent's state.json. Used by --json to surface both halves of the
// transition (current.source and target.source). Returns "" when the
// file is missing or the field is absent — never errors.
func readStateTopLevel(name, targetRoot, field string) string {
	data, err := os.ReadFile(filepath.Join(targetRoot, "state", name+".state.json"))
	if err != nil {
		return ""
	}
	var state map[string]any
	if err := jsonUnmarshal(data, &state); err != nil {
		return ""
	}
	return asString(state[field])
}

// planRollbackJSON is the cobra entry point for `agentpkg rollback --json`.
// It reads state.json, resolves the rollback target, and writes a JSON
// plan to stdout — without touching the marketplace API, downloading
// the target tarball, or running any script. The plan schema is:
//
//	{
//	  "agent": "opencode",
//	  "current": { "version": "1.18.9", "source": "upstream", "channel": "stable" },
//	  "target":  { "version": "1.18.5", "source": "upstream", "channel": "stable",
//	               "to_version_cli_override": "",
//	               "to_source_cli_override":  "" },
//	  "target_root": "/home/u/.local",
//	  "inverse_migration": {
//	    "expected_script_basename": "to-1.18.9.sh",
//	    "will_run": "unknown"     // "unknown" because presence check requires tarball
//	  },
//	  "steps": [
//	    "resolve target tarball + verify sha256",
//	    "run migrate/to-<from>.sh if present (best-effort, failures WARN)",
//	    "run target install.sh",
//	    "write/update systemd --user units (skipped with --no-services)",
//	    "append services/configs/config_dir to state.json"
//	  ],
//	  "exit_code_zero_on_success": true,
//	  "note": "--json does not contact the marketplace-api and does not inspect the target tarball."
//	}
//
// Failure modes map to the same exit codes as the non-JSON path:
// state.json missing or .previous empty → 74.
func planRollbackJSON(name string, s *installShared, toVersion, toSource string) error {
	targetRoot := s.targetRoot
	if targetRoot == "" {
		targetRoot = filepath.Join(userHomeOrTmp(), ".local")
	}
	currentVersion, targetV, targetSrc, targetCh, err := readStatePrevious(name, targetRoot)
	if err != nil {
		// Surface the same exit-code semantics as runRollback by returning
		// the typed rollbackError. main.go translates to os.Exit.
		return err
	}
	// current.source isn't part of readStatePrevious's return — pull it
	// out of the same state.json so the JSON plan can show both halves
	// of the transition.
	currentSource := readStateTopLevel(name, targetRoot, "source")
	if toVersion != "" {
		targetV = toVersion
	}
	if toSource != "" {
		targetSrc = toSource
	}
	if targetV == "" || targetSrc == "" {
		return wrapRollbackErr(ExitRollbackNoTarget, nil,
			"rollback target not fully resolved: version=%q source=%q", targetV, targetSrc)
	}
	effectiveCh := s.channel
	if effectiveCh == "" {
		if targetCh != "" {
			effectiveCh = targetCh
		} else {
			effectiveCh = "stable"
		}
	}
	effectiveSrc := s.source
	if effectiveSrc == "" {
		effectiveSrc = targetSrc
	}

	// expected_script_basename documents what agentpkg WILL look for
	// inside the target tarball once it downloads it. The actual
	// "will_run: true|false" requires extracting the tarball — we
	// intentionally don't do that here; --json is a static plan.
	expectedBase := "to-" + currentVersion + ".sh"
	if currentVersion == "" {
		expectedBase = "(unknown — no current version recorded)"
	}

	plan := map[string]any{
		"agent": name,
		"current": map[string]any{
			"version": currentVersion,
			"source":  currentSource,
		},
		"target": map[string]any{
			"version":                 targetV,
			"source":                  effectiveSrc,
			"channel":                 effectiveCh,
			"to_version_cli_override": toVersion,
			"to_source_cli_override":  toSource,
		},
		"target_root": targetRoot,
		"inverse_migration": map[string]any{
			"expected_script_basename": expectedBase,
			"will_run":                 "unknown (requires tarball inspection)",
		},
		"steps": []string{
			"resolve target tarball + verify sha256",
			"run migrate/to-<from>.sh if present (best-effort, failures WARN)",
			"run target install.sh",
			"write/update systemd --user units (skipped with --no-services)",
			"append services/configs/config_dir to state.json",
		},
		"exit_code_zero_on_success": true,
		"note":                      "--json does not contact the marketplace-api and does not inspect the target tarball.",
	}
	out, err := jsonMarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal rollback plan: %w", err)
	}
	fmt.Println(string(out))
	return nil
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
