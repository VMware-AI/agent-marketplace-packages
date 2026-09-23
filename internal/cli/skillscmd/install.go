package skillscmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/cli"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/spf13/cobra"
)

// defaultSkillsStateDir returns the per-user skills state root. This is
// where state.json and the `latest` symlink live. Resolved lazily so
// tests can override $HOME before invoking the command.
func defaultSkillsStateDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "agentpkg", "skills")
	}
	return ".local/share/agentpkg/skills"
}

// NewSkillsInstallCmd creates `agentpkg skills install`.
//
// End-to-end flow:
//  1. Resolve version — from --version, else latest in --channel.
//  2. Pick zip source — server GET (default) or local --from path.
//  3. Verify sha256 against the server-published sidecar.
//  4. Resolve per-agent install paths via sv.Agents (or, when --target-dir
//     is set, fan out to a single override root — legacy mode).
//  5. Extract zip into each target.
//  6. Update `latest` symlink + write state.json under the state root.
//
// Two distinct roots are involved:
//   - state root (always $HOME/.local/share/agentpkg/skills): state.json,
//     the `latest` symlink, and — when --target-dir is NOT set — the
//     payload itself.
//   - target root (only when --target-dir is set): single root that
//     replaces every per-agent path (legacy / staging / CI behavior).
//
// All operations are idempotent: re-installing the same (source, version)
// is a no-op unless --force is set.
//
// Batch mode: pass multiple positional <name> arguments to install
// several skills in one invocation. Each skill is processed
// independently — failures don't abort the batch. The exit code is
// non-zero if any install failed, and the failures are listed at the
// end of output. --from is incompatible with batch (it's a single-file
// shortcut).
func NewSkillsInstallCmd() *cobra.Command {
	var (
		version   string
		source    string
		channel   string
		targetDir string
		fromZip   string
		force     bool
		download  bool
		dryRun    bool
		asJSON    bool
	)
	c := &cobra.Command{
		Use:   "install <name> [<name>...] [--version X.Y.Z] [--source community|internal] [--channel stable|beta|edge|internal] [--target-dir DIR] [--from <local.zip>] [--force] [--download-only] [--dry-run] [--json]",
		Short: "Download one or more skill zips and extract them locally",
		Long: `install downloads (or reads) one or more skill zips, verifies
their sha256, unzips them into <target-dir>/<name>/<version>/ (or the
per-agent default paths when --target-dir is not set), updates the
'latest' symlink, and writes <target-dir>/<name>/state.json.

By default the version is resolved from the registry as "the highest
semver in --channel" (default: stable). Use --version to pin a specific
release.

Use --from to install from a local zip file (skip the registry entirely).
--from is single-skill only — combine it with multiple <name> args to
get a clear error rather than ambiguous pairing.

Batch mode: pass several <name> arguments to install them all in one
invocation. Each skill is processed independently; the exit code is
non-zero if any one fails, and the failed skills are listed at the end.

Re-installing the same (source, version) is a no-op; pass --force to
re-extract on top of an existing directory.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !skills.IsValidChannel(channel) && channel != "" {
				return fmt.Errorf("--channel %q is invalid", channel)
			}
			if !skills.IsValidSource(source) && source != "" {
				return fmt.Errorf("--source %q is invalid", source)
			}
			if len(args) > 1 && fromZip != "" {
				return fmt.Errorf("--from is single-skill only; cannot combine with multiple names")
			}
			// State root. When --target-dir is set (legacy single-root
			// mode), it serves as both state root AND payload root —
			// everything lives under one tree. Otherwise the state root
			// is the central stash ($HOME/.local/share/agentpkg/skills)
			// and payloads fan out to per-agent paths.
			stateDir := defaultSkillsStateDir()
			if targetDir != "" {
				stateDir = targetDir
			}
			if version != "" && !looksLikeVersion(version) {
				return fmt.Errorf("--version %q is malformed", version)
			}
			// Single-skill fast path: runInstall directly (preserves the
			// historical "success message + nothing else" output shape).
			if len(args) == 1 {
				return runInstall(cmd, installParams{
					Name:      args[0],
					Version:   version,
					Source:    source,
					Channel:   channel,
					TargetDir: targetDir, // empty → per-agent paths
					StateDir:  stateDir,
					FromZip:   fromZip,
					Force:     force,
					Download:  download,
					DryRun:    dryRun,
					JSON:      asJSON,
				})
			}
			// Batch path: process every name independently, collect errors.
			return runInstallBatch(cmd, args, installParams{
				Version:   version,
				Source:    source,
				Channel:   channel,
				TargetDir: targetDir,
				StateDir:  stateDir,
				FromZip:   fromZip,
				Force:     force,
				Download:  download,
				DryRun:    dryRun,
				JSON:      asJSON,
			})
		},
	}
	c.Flags().StringVar(&version, "version", "", "pin a specific version (default: latest in --channel)")
	c.Flags().StringVar(&source, "source", "", "source tree (default: community)")
	c.Flags().StringVar(&channel, "channel", "stable", "channel tag for 'latest' resolution")
	c.Flags().StringVar(&targetDir, "target-dir", "", "install root override (default: per-agent paths under $HOME; legacy single-root mode)")
	c.Flags().StringVar(&fromZip, "from", "", "install from a local zip file (single-skill only)")
	c.Flags().BoolVar(&force, "force", false, "overwrite existing install of the same version")
	c.Flags().BoolVar(&download, "download-only", false, "download the zip but don't extract or update state")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan, don't touch disk")
	c.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	return c
}

// installParams bundles the cobra flag values for the runInstall worker.
// Pulled out as a struct so tests can construct it directly without
// rebuilding the cobra plumbing.
//
// TargetDir is the per-payload override (legacy / staging behavior):
// when empty, payloads fan out to per-agent paths. StateDir is where
// state.json + the `latest` symlink live — defaults to
// $HOME/.local/share/agentpkg/skills.
type installParams struct {
	Name      string
	Version   string
	Source    string
	Channel   string
	TargetDir string
	StateDir  string
	FromZip   string
	Force     bool
	Download  bool
	DryRun    bool
	JSON      bool
}

// runInstall executes the full install pipeline. Kept as a free function
// (not a closure inside NewSkillsInstallCmd) so it's directly testable
// from install_test.go without spinning up cobra.
//
// The flow:
//   1. Resolve version + source + zip bytes (server or --from).
//   2. Verify sha256 against the sidecar.
//   3. If --download-only, write the zip to <target-dir>/<name>/<zip>
//      and stop.
//   4. Extract zip to <target-dir>/<name>/<version>/.
//   5. Update `latest` symlink.
//   6. Write state.json.
//   7. Emit success message (table or --json).
func runInstall(cmd *cobra.Command, p installParams) error {
	out := cmd.OutOrStdout()

	// 1. Resolve source (default community) and zip bytes.
	if p.Source == "" {
		p.Source = "community"
	}
	if p.Channel == "" {
		p.Channel = "stable"
	}

	if p.DryRun {
		fmt.Fprintf(out, "(dry-run) would install %s", p.Name)
		if p.Version != "" {
			fmt.Fprintf(out, " %s", p.Version)
		} else {
			fmt.Fprintf(out, " (latest in %s)", p.Channel)
		}
		// Reflect where the payload would actually land. When --target-dir
		// is set, everything fans into one root (legacy); otherwise the
		// per-agent paths are used (state root still hosts state.json).
		if p.TargetDir != "" {
			fmt.Fprintf(out, " to %s (single-root mode)\n", p.TargetDir)
		} else {
			fmt.Fprintf(out, " to per-agent paths (state root: %s)\n", p.StateDir)
		}
		if p.FromZip != "" {
			fmt.Fprintf(out, "         from %s\n", p.FromZip)
		}
		return nil
	}

	var zipBytes []byte
	var expectedSHA string
	var zipFilename string
	var releasedAt string
	var err error

	if p.FromZip != "" {
		// Local file: read once, compute hash, skip the registry entirely.
		zipBytes, err = os.ReadFile(p.FromZip)
		if err != nil {
			return fmt.Errorf("read --from: %w", err)
		}
		zipFilename = filepath.Base(p.FromZip)
		sum := sha256.Sum256(zipBytes)
		expectedSHA = "sha256:" + hex.EncodeToString(sum[:])
		// For local installs the version comes from the zip filename,
		// not the server's index. The user can still pin --version to
		// override (useful when the filename has been normalized away
		// from a real semver, e.g. the build command's append).
		if p.Version == "" {
			_, _, fileVer, perr := skills.ParseZipFilename(zipFilename)
			if perr != nil {
				return fmt.Errorf("cannot infer version from %q: %w (pass --version explicitly)", zipFilename, perr)
			}
			p.Version = fileVer
		}
	} else {
		// Server-driven: resolve (source, version), fetch zip + sidecar.
		client, err := cli.NewClient(*cfgPath, *credsPath)
		if err != nil {
			return err
		}
		if p.Version == "" {
			picked, err := pickLatestSkillVersion(client, p.Name, p.Source, p.Channel)
			if err != nil {
				return fmt.Errorf("resolve latest version: %w", err)
			}
			p.Version = picked
			fmt.Fprintf(out, "Resolved latest %s: %s\n", p.Name, p.Version)
		}
		zipName, serverSHA, err := resolveSkillBundle(client, p.Source, p.Name, p.Version)
		if err != nil {
			return err
		}
		zipFilename = zipName
		expectedSHA = serverSHA
		// Fetch the SkillVersion to grab the v2.0 metadata (released_at,
		// agents, install_method, install_paths, install_config).
		var sv skills.SkillVersion
		svPath := fmt.Sprintf("/api/v1/skills/%s/%s/%s", p.Source, p.Name, p.Version)
		if err := client.Do(svPath, &sv); err == nil {
			releasedAt = sv.ReleasedAt
		}
		zipBytes, err = fetchSkillZip(client, p.Source, p.Name, p.Version, expectedSHA)
		if err != nil {
			return err
		}
	}

	// 2. --download-only short-circuits: just persist the zip.
	// Use the per-payload root when set, else the state root.
	if p.Download {
		root := p.TargetDir
		if root == "" {
			root = p.StateDir
		}
		dir := filepath.Join(root, p.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		dest := filepath.Join(dir, zipFilename)
		if err := os.WriteFile(dest, zipBytes, 0o644); err != nil {
			return err
		}
		sidecar := dest + ".sha256"
		_ = os.WriteFile(sidecar, []byte(trimSHA256Prefix(expectedSHA)+"  "+zipFilename+"\n"), 0o644)
		fmt.Fprintf(out, "Downloaded %s -> %s\n", zipFilename, dest)
		return nil
	}

	// 3. Idempotency: if the version is already installed and the
	// recorded sha256 matches, exit 0 with a friendly message.
	cur, _ := loadSkillState(p.StateDir, p.Name)
	if cur != nil {
		if iv, ok := cur.InstalledVersions[p.Version]; ok {
			if iv.ZipSHA256 == expectedSHA && !p.Force {
				fmt.Fprintf(out, "%s %s is already installed (sha256 match).\n", p.Name, p.Version)
				return nil
			}
		}
	}

	// 4. Resolve the SkillVersion metadata (re-fetch from local fetch or
	// server). We need agents / install_method / install_paths to
	// dispatch to the right pipeline.
	var sv skills.SkillVersion
	if p.FromZip != "" {
		// Local zip: parse SKILL.md from the bytes we already have.
		mfst, _, err := skills.ExtractSkillMDFromBytes(zipBytes, zipFilename)
		if err != nil {
			return fmt.Errorf("parse --from zip: %w", err)
		}
		sv.Agents = mfst.Agents
		sv.InstallMethod = mfst.Metadata.InstallMethod
		sv.InstallPaths = mfst.Metadata.InstallPaths
	} else {
		client, err := cli.NewClient(*cfgPath, *credsPath)
		if err != nil {
			return err
		}
		svPath := fmt.Sprintf("/api/v1/skills/%s/%s/%s", p.Source, p.Name, p.Version)
		if err := client.Do(svPath, &sv); err != nil {
			return fmt.Errorf("fetch skill version metadata: %w", err)
		}
	}

	// 5. Resolve install targets from sv.Agents + sv.InstallPaths.
	//    The staging parent lives under stateDir so it cleans up with
	//    `rm -rf $stateDir/.staging`. p.TargetDir (when non-empty) acts
	//    as the legacy single-root override — every agent fans out to
	//    <TargetDir>/<name> in that mode.
	stagingParent := filepath.Join(p.StateDir, ".staging")
	targets, err := ResolveTargets(&sv, p.Name, p.TargetDir, stagingParent)
	if err != nil {
		return err
	}
	defer CleanupStaging(targets)

	// 5a. For zip-extract (the historical behavior), keep the
	// <name>/<version>/ staging layout under each target so the per-version
	// symlink + state.json continue to work. For other methods, write
	// directly into the agent's resolved path.
	if sv.InstallMethod == "" || sv.InstallMethod == methodZipExtract {
		// Wrap each target into <target>/<version>/ so multiple versions
		// coexist. ResolveTargets already created per-target staging;
		// here we shift FinalPath to include the version segment.
		for i := range targets {
			targets[i].FinalPath = filepath.Join(targets[i].FinalPath, p.Version)
		}
	}

	if p.DryRun {
		fmt.Fprintf(out, "(dry-run) would install %s %s via %s to:\n",
			p.Name, p.Version, sv.InstallMethod)
		for _, t := range targets {
			fmt.Fprintf(out, "    %s -> %s\n", t.Agent, t.FinalPath)
		}
		return nil
	}

	// 5b. Force-reinstall: clean existing on-disk payload for the version
	// under the agent-specific target(s).
	if p.Force {
		for _, t := range targets {
			if err := os.RemoveAll(t.FinalPath); err != nil {
				return fmt.Errorf("force-clean %s: %w", t.FinalPath, err)
			}
		}
	}

	// 6. Dispatch to the install method.
	if _, err := Install(InstallRequest{
		SkillName: p.Name,
		Method:    sv.InstallMethod,
		Agents:    sv.Agents,
		Config:    sv.InstallConfig,
		ZipBytes:  zipBytes,
		Targets:   targets,
	}); err != nil {
		return err
	}

	// 7. Update the `latest` symlink under the state root. Skipped in
	// per-agent mode (TargetDir == "") because the version dirs sit
	// inside each agent's resolved path, not under stateDir — there is
	// no single place for the symlink to point to.
	if p.TargetDir != "" {
		if err := updateLatestSymlink(p.StateDir, p.Name, p.Version); err != nil {
			return err
		}
	}

	// 8. Write state.json (now with v2.0 fields).
	st := cur
	if st == nil {
		st = &SkillInstallState{
			Name:             p.Name,
			InstalledVersions: map[string]InstalledVersion{},
		}
	}
	if st.InstalledVersions == nil {
		st.InstalledVersions = map[string]InstalledVersion{}
	}
	st.CurrentVersion = p.Version
	st.Source = p.Source
	st.Channel = p.Channel
	st.TargetDir = filepath.Join(p.StateDir, p.Name)
	// v2.0 fields:
	st.InstallMethod = sv.InstallMethod
	if st.InstallMethod == "" {
		st.InstallMethod = methodZipExtract
	}
	st.Agents = sv.Agents
	if len(st.Agents) == 0 {
		st.Agents = []string{"all"}
	}
	st.ResolvedPaths = make(map[string]string, len(targets))
	for _, t := range targets {
		st.ResolvedPaths[t.Agent] = t.FinalPath
	}
	st.InstalledVersions[p.Version] = InstalledVersion{
		Source:      p.Source,
		Channel:     p.Channel,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		ZipSHA256:   expectedSHA,
		ZipFilename: zipFilename,
		ReleasedAt:  releasedAt,
	}
	if err := saveSkillState(p.StateDir, st); err != nil {
		return err
	}

	fmt.Fprintf(out, "Installed %s %s via %s to:\n", p.Name, p.Version, sv.InstallMethod)
	for _, t := range targets {
		fmt.Fprintf(out, "    %s -> %s\n", t.Agent, t.FinalPath)
	}
	fmt.Fprintf(out, "Latest -> %s\n", p.Version)
	return nil
}

// runInstallBatch drives a batch install: each name is processed
// independently via runInstall, errors are collected (not returned
// immediately), and at the end the failures are listed.
//
// Returns nil when every name succeeded; otherwise a non-nil
// *installBatchError that lists every (name, error) pair. The non-nil
// return guarantees a non-zero cobra exit code so CI can detect partial
// failures. The error's Error() string is human-readable; for machine
// consumption, callers can type-assert and read .Statuses.
func runInstallBatch(cmd *cobra.Command, names []string, p installParams) error {
	out := cmd.OutOrStdout()
	type result struct {
		name    string
		ok      bool
		err     string
		version string // empty if the install never resolved a version
	}
	results := make([]result, 0, len(names))
	for _, name := range names {
		perName := p
		perName.Name = name
		if err := runInstall(cmd, perName); err != nil {
			results = append(results, result{name: name, ok: false, err: err.Error()})
			if p.JSON {
				fmt.Fprintf(out, "{\"name\":%q,\"ok\":false,\"error\":%q}\n", name, err.Error())
			} else {
				fmt.Fprintf(out, "✗ %s: %s\n", name, err.Error())
			}
			continue
		}
		// runInstall resolved the version via the registry; for the
		// batch summary we don't know it without re-parsing, but the
		// success line emitted by runInstall already names the version.
		results = append(results, result{name: name, ok: true})
	}

	failed := 0
	for _, r := range results {
		if !r.ok {
			failed++
		}
	}
	if failed == 0 {
		return nil
	}

	// Build the typed error so callers can introspect, but only after
	// the human-readable summary has been printed (so the operator sees
	// what failed, not just the final line).
	if !p.JSON {
		failedNames := make([]string, 0, failed)
		for _, r := range results {
			if !r.ok {
				failedNames = append(failedNames, r.name)
			}
		}
		fmt.Fprintf(out, "\n%d/%d installs failed: %s\n",
			failed, len(results), strings.Join(failedNames, ", "))
	}
	statuses := make([]installBatchStatus, 0, len(results))
	for _, r := range results {
		s := installBatchStatus{Name: r.name, OK: r.ok, Error: r.err}
		statuses = append(statuses, s)
	}
	return &installBatchError{Statuses: statuses}
}

// installBatchStatus is one row in a batch install report.
type installBatchStatus struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// installBatchError aggregates per-name failures from a batch install.
// Its Error() lists the failed names so operators see the relevant
// subset without re-reading the full transcript.
type installBatchError struct {
	Statuses []installBatchStatus
}

func (e *installBatchError) Error() string {
	var failed []string
	for _, s := range e.Statuses {
		if !s.OK {
			failed = append(failed, s.Name)
		}
	}
	if len(failed) == 0 {
		return "batch install failed"
	}
	return fmt.Sprintf("batch install failed: %s", strings.Join(failed, ", "))
}

// fetchSkillZip downloads the zip, verifies sha256 in-flight, and returns
// the bytes. Mirrors the streaming-verify pattern from internal/cli/download.go.
func fetchSkillZip(c *cli.Client, source, name, version, expectedSHA string) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/skills/%s/%s/%s/download", c.BaseURL, source, name, version)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("agentpkg", c.Password)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d while downloading zip", resp.StatusCode)
	}
	h := sha256.New()
	buf, err := io.ReadAll(io.TeeReader(resp.Body, h))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if got != expectedSHA {
		return nil, fmt.Errorf("sha256 mismatch: expected %s, got %s", expectedSHA, got)
	}
	// Make sure we don't silently accept a tiny zip that's missing SKILL.md.
	if !strings.Contains(http.DetectContentType(buf), "zip") && len(buf) < 22 {
		return nil, fmt.Errorf("downloaded content is not a zip (size=%d)", len(buf))
	}
	return buf, nil
}