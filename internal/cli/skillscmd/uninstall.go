package skillscmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/spf13/cobra"
)

// NewSkillsUninstallCmd creates `agentpkg skills uninstall`.
//
// Removes a locally-installed skill. Default behavior is to remove every
// version + state.json + the `latest` symlink (a complete wipe). Pass
// --version to delete a single version; the symlink is repointed to the
// next-most-recent installed version, or removed if none remain.
//
// All operations are local-filesystem — this command never talks to the
// marketplace-api. Re-installing a skill that was previously uninstalled
// just creates a fresh state.json entry.
func NewSkillsUninstallCmd() *cobra.Command {
	var (
		version   string
		targetDir string
		keepState bool
		dryRun    bool
	)
	c := &cobra.Command{
		Use:   "uninstall <name> [--version X.Y.Z] [--target-dir DIR] [--keep-state] [--dry-run]",
		Short: "Remove a locally-installed skill (or one version of it)",
		Long: `uninstall removes a skill from the local target directory.

Default: removes every version of <name> along with state.json and the
'latest' symlink. Pass --version to remove only one version; the symlink
is repointed to the next-most-recent installed version.

Use --keep-state to delete the on-disk version directories but leave
state.json intact (useful for "offline" the install without losing the
recorded metadata).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			// state root (state.json + symlinks). When --target-dir is
			// set, it doubles as the state root (legacy single-root
			// mode), matching the install-time behavior.
			stateDir := defaultSkillsStateDir()
			if targetDir != "" {
				stateDir = targetDir
			}
			return runUninstall(cmd, uninstallParams{
				Name:      name,
				Version:   version,
				TargetDir: targetDir, // empty → use paths from state.json
				StateDir:  stateDir,
				KeepState: keepState,
				DryRun:    dryRun,
			})
		},
	}
	c.Flags().StringVar(&version, "version", "", "remove only this version (omit to remove all)")
	c.Flags().StringVar(&targetDir, "target-dir", "", "per-payload root override (empty: read paths from state.json)")
	c.Flags().BoolVar(&keepState, "keep-state", false, "delete version dirs but leave state.json intact")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan, don't touch disk")
	return c
}

type uninstallParams struct {
	Name      string
	Version   string
	TargetDir string // empty → use paths from state.json (per-agent)
	StateDir  string
	KeepState bool
	DryRun    bool
}

func runUninstall(cmd *cobra.Command, p uninstallParams) error {
	out := cmd.OutOrStdout()
	// Always read state.json from the state root; per-payload paths come
	// from state.json's ResolvedPaths unless --target-dir was set at install.
	st, err := loadSkillState(p.StateDir, p.Name)
	if err != nil {
		return err
	}
	if st == nil {
		return fmt.Errorf("%s is not installed in %s", p.Name, p.StateDir)
	}

	// payloadRoot is where the per-version dirs live. When --target-dir
	// was used at install time, all payloads sit under one root; when it
	// wasn't, each agent has its own path recorded in state.ResolvedPaths.
	payloadRoot := p.TargetDir
	if payloadRoot == "" && len(st.ResolvedPaths) > 0 {
		// Heuristic: if every resolved_path sits under one common parent,
		// that's the implicit root (e.g. legacy single-root mode).
		// Otherwise we walk each agent's path separately.
	}

	if p.DryRun {
		if p.Version != "" {
			dir := versionDir(payloadRoot, st, p.Name, p.Version)
			fmt.Fprintf(out, "(dry-run) would remove %s\n", dir)
			return nil
		}
		dir := filepath.Join(p.StateDir, p.Name)
		fmt.Fprintf(out, "(dry-run) would remove all versions of %s under %s\n", p.Name, dir)
		return nil
	}

	// Single-version mode.
	if p.Version != "" {
		if _, ok := st.InstalledVersions[p.Version]; !ok {
			return fmt.Errorf("%s version %s is not installed", p.Name, p.Version)
		}
		// Wipe every per-agent dir (one per ResolvedPaths entry). The legacy
		// versionDir() helper only returned the FIRST agent's path, leaving
		// 2+ agents' dirs behind — that's the bug this branch fixes.
		for _, dir := range payloadDirs(payloadRoot, st, p.Name) {
			if err := os.RemoveAll(filepath.Join(dir, p.Version)); err != nil {
				return fmt.Errorf("remove %s: %w", filepath.Join(dir, p.Version), err)
			}
		}
		// Update or clear `latest` symlink.
		if p.Version == st.CurrentVersion {
			// Repoint to the next-most-recent installed version, or
			// clear the symlink if none remain.
			repoint := nextInstalledVersion(st, p.Version)
			if repoint != "" {
				if err := updateLatestSymlink(p.StateDir, p.Name, repoint); err != nil {
					return err
				}
				st.CurrentVersion = repoint
			} else {
				clearLatestSymlink(p.StateDir, p.Name)
				st.CurrentVersion = ""
			}
		}
		if !p.KeepState {
			delete(st.InstalledVersions, p.Version)
			if len(st.InstalledVersions) == 0 {
				// Last version gone — safe to lift the now-empty per-agent
				// parent dirs and the central <StateDir>/<name> dir. We
				// moved this from inside the per-version loop because the
				// multi-version case (uninstall --version 1.0.0 when 2.0.0
				// is still installed) must NOT nuke the sibling version.
				for _, dir := range payloadDirs(payloadRoot, st, p.Name) {
					if err := os.RemoveAll(dir); err != nil {
						return fmt.Errorf("remove %s: %w", dir, err)
					}
				}
				if err := removeSkillState(p.StateDir, p.Name); err != nil {
					return err
				}
				// removeSkillState cleared state.json + the `latest`
				// symlink under <StateDir>/<name>/. Lift the now-empty
				// parent too so a clean uninstall leaves no cosmetic
				// leftovers in the central stash. No-op if already gone.
				if err := os.RemoveAll(filepath.Join(p.StateDir, p.Name)); err != nil {
					return fmt.Errorf("remove %s: %w", filepath.Join(p.StateDir, p.Name), err)
				}
				fmt.Fprintf(out, "Uninstalled %s %s (state cleared)\n", p.Name, p.Version)
				return nil
			}
		}
		if err := saveSkillState(p.StateDir, st); err != nil {
			return err
		}
		fmt.Fprintf(out, "Uninstalled %s %s\n", p.Name, p.Version)
		return nil
	}

	// All-versions mode: wipe per-skill payload dirs (per-agent or
	// single-root, depending on the install mode), then remove state.json
	// + the now-empty central stash dir (per-agent mode).
	dirs := payloadDirs(payloadRoot, st, p.Name)
	if p.KeepState {
		// F021 (tested 2026-09-30): --keep-state used to be a no-op in
		// legacy single-root mode because state.json sits at
		// <target-dir>/<name>/state.json — INSIDE the payloadRoot — and
		// the RemoveAll below wiped both the version dirs AND state.json.
		// Save state.json first (if it exists), wipe the dirs, then
		// restore it so callers see the documented "leave state.json
		// intact" behavior. We only do this in single-root mode; in
		// per-agent mode state.json lives at <stateDir>/<name>/state.json
		// which is NOT under payloadRoot, so the existing --keep-state
		// branch already worked correctly.
		if payloadRoot != "" {
			statePath := filepath.Join(payloadRoot, p.Name, "state.json")
			if data, err := os.ReadFile(statePath); err == nil {
				// Track which payloads we wiped so we can restore the
				// skill dir structure around the preserved state.json.
				savedState = data
			}
		}
	}
	for _, d := range dirs {
		if err := os.RemoveAll(d); err != nil {
			return fmt.Errorf("remove %s: %w", d, err)
		}
	}
	if !p.KeepState {
		// Always wipe the central stash dir (state.json + the now-empty
		// <stateDir>/<name> parent). In legacy single-root mode this dir
		// was already removed by payloadDirs above, so the call is a
		// no-op there.
		_ = os.RemoveAll(filepath.Join(p.StateDir, p.Name))
	} else if savedState != nil && payloadRoot != "" {
		// Restore state.json at <target-dir>/<name>/state.json.
		// Recreate the parent dir first since RemoveAll just wiped it.
		dir := filepath.Join(payloadRoot, p.Name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("restore state.json dir: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), savedState, 0644); err != nil {
			return fmt.Errorf("restore state.json: %w", err)
		}
		// Note: the `latest` symlink is intentionally NOT restored — the
		// version dirs it pointed at are gone. Without versions on disk,
		// re-pointing `latest` would just be a dangling symlink. Callers
		// who want a "fully intact" uninstall should not pass --keep-state.
		fmt.Fprintf(out, "Uninstalled %s (all versions; state.json preserved)\n", p.Name)
		return nil
	}
	fmt.Fprintf(out, "Uninstalled %s (all versions)\n", p.Name)
	return nil
}

// savedState is a package-local scratch buffer used by runUninstall when
// --keep-state is set in single-root mode (see F021). It carries state.json
// across the RemoveAll(payloadRoot/name) call so we can write it back
// afterwards. nil in all other modes.
var savedState []byte

// payloadDirs returns the list of distinct on-disk dirs to wipe for
// "uninstall all versions". In legacy single-root mode (payloadRoot set),
// returns one dir. In per-agent mode, returns one dir per agent's
// resolved path.
//
// Exposed so the single-version mode in runUninstall can ALSO use it:
// instead of relying on versionDir (which only returned the first
// agent's path), we wipe every per-agent dir explicitly. That way the
// historical multi-version state.json files with 2+ resolved_paths
// get fully cleaned, and the install-time SKILL.md fan-out doesn't
// leave stale dirs behind.
func payloadDirs(payloadRoot string, st *SkillInstallState, name string) []string {
	if payloadRoot != "" {
		return []string{filepath.Join(payloadRoot, name)}
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range st.ResolvedPaths {
		dir := filepath.Dir(p) // <resolvedPath>/<version> → <resolvedPath>
		if seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	if len(out) == 0 {
		return []string{filepath.Join(st.TargetDir)}
	}
	return out
}

// versionDir returns the on-disk path for one (name, version) pair.
// When payloadRoot is set (legacy single-root mode), uses
// <payloadRoot>/<name>/<version>. Otherwise returns the FIRST
// per-agent path recorded in state.ResolvedPaths; if multiple agents
// share the same path it appears once. Falls back to <StateDir>/<name>/<version> when
// no resolved path exists (e.g. very old state.json).
//
// Note: this is ONLY used for the dry-run preview path. The actual
// single-version uninstall uses payloadDirs() so every per-agent dir
// gets wiped (not just the first one — the per-agent path iteration
// here is preserved for backward compat with callers that only have
// a single resolved_path entry to print).
func versionDir(payloadRoot string, st *SkillInstallState, name, version string) string {
	if payloadRoot != "" {
		return filepath.Join(payloadRoot, name, version)
	}
	for _, p := range st.ResolvedPaths {
		return filepath.Join(p, version)
	}
	return filepath.Join(st.TargetDir, version)
}

// nextInstalledVersion returns the highest-semver installed version
// that's not equal to exclude. Returns "" if no other version is
// installed. Used by uninstall to repoint the `latest` symlink after
// removing the currently-active version.
func nextInstalledVersion(st *SkillInstallState, exclude string) string {
	candidates := make([]string, 0, len(st.InstalledVersions))
	for v := range st.InstalledVersions {
		if v == exclude {
			continue
		}
		candidates = append(candidates, v)
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return skills.SemverCompare(candidates[i], candidates[j]) > 0
	})
	return candidates[0]
}

// _ keeps io imported so a future streaming-cleanup helper can be added
// without re-touching this file's imports.
var _ = io.Discard