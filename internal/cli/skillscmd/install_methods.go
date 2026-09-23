package skillscmd

// install_methods.go — the dispatch layer that materializes a skill zip
// onto the target machine via one of four pipelines:
//
//   - zip-extract:    extract the zip verbatim into FinalPath (the
//                     historical behavior; default for legacy zips)
//   - pip-wheel:      uv pip install --no-index --find-links from a
//                     `wheels/` dir inside the extracted zip
//   - npm-pack:       `npm install -g <package>` using install_config
//   - tarball:        the zip contains a single .tar.gz payload; extract
//                     it into FinalPath
//
// The dispatcher (`Install`) also resolves per-agent install paths via
// `resolvedInstallPaths` — see the install_paths schema field for how
// authors override the built-in agent→path table.
//
// All four methods share the same contract:
//   in:  InstallRequest (the SkillVersion + zip bytes + install config)
//   out: []InstallTarget (the on-disk paths actually written)
//
// On error, the method must clean up any partial output it produced.
// State.json is written by the caller (runInstall) only after all
// methods succeed — so partial-install cleanup is straightforward.

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
)

// installMethod IDs. Keep these in sync with internal/skills/catalogs.go.
const (
	methodZipExtract = "zip-extract"
	methodPipWheel   = "pip-wheel"
	methodNpmPack    = "npm-pack"
	methodTarball    = "tarball"
)

// InstallRequest is the input to the dispatcher.
//
// ZipBytes is the raw zip payload from the registry (or from --from).
// It's required for zip-extract, pip-wheel (wheels live inside), npm-pack
// (package.json lives inside), and tarball (the .tar.gz lives inside).
//
// Method selects the pipeline. Agents declares the runtimes the skill
// targets; Targets is the resolved per-agent FinalPath list (already
// $HOME/$NAME-substituted).
type InstallRequest struct {
	SkillName string
	Method    string
	Agents    []string
	Config    map[string]any
	ZipBytes  []byte
	Targets   []InstallTarget
}

// InstallTarget is one resolved on-disk destination.
type InstallTarget struct {
	// Agent is the runtime ID this target serves ("all" for the central
	// fallback path). Distinguishing this lets uninstall know which
	// agent's directory to clean up.
	Agent     string
	FinalPath string
	// StagingDir is a temporary directory the install method can use
	// for extraction / scratch space. Created by the dispatcher; the
	// install method MAY write into it; the dispatcher cleans it up.
	StagingDir string
}

// Install dispatches to the right pipeline based on req.Method.
//
// Returns the targets it actually wrote to. On error, partial output
// is rolled back (best-effort) before returning.
func Install(req InstallRequest) ([]InstallTarget, error) {
	if req.ZipBytes == nil && req.Method != methodNpmPack {
		// npm-pack can technically run without a payload (uses install_config.package)
		// but the other three need bytes.
		return nil, fmt.Errorf("skills: install %q requires a zip payload", req.Method)
	}
	switch req.Method {
	case methodZipExtract, "":
		return installZipExtract(req)
	case methodPipWheel:
		return installPipWheel(req)
	case methodNpmPack:
		return installNpmPack(req)
	case methodTarball:
		return installTarball(req)
	default:
		return nil, fmt.Errorf("skills: install_method %q is not implemented", req.Method)
	}
}

// installZipExtract extracts the zip to every target's FinalPath.
//
// Multi-agent fan-out: a skill with agents=[opencode, openclaw, hermes]
// lands in all three of their default install paths. The dedup logic in
// ResolveTargets already collapses coincident paths (legacy
// --target-dir single-root mode), so this loop simply visits each
// unique FinalPath once.
//
// Idempotency: existing files in each target are overwritten (the
// caller's --force flag controls pre-clean).
func installZipExtract(req InstallRequest) ([]InstallTarget, error) {
	for _, t := range req.Targets {
		if err := skills.ExtractAllFromBytes(req.ZipBytes, t.FinalPath); err != nil {
			return nil, fmt.Errorf("skills: zip-extract failed (agent=%s -> %s): %w", t.Agent, t.FinalPath, err)
		}
	}
	return req.Targets, nil
}

// installPipWheel extracts the zip to a staging dir, locates the wheels/
// subdirectory (the convention from hermes-agent payloads), and runs
// `uv pip install --no-index --find-links <staging>/wheels`.
//
// The exact wheel name is taken from install_config.wheel when set; the
// find-links index covers transitive dependencies in the same dir.
//
// Only the first target is used (system-wide pip install); per-agent
// "pip install into a per-agent venv" is a future extension.
func installPipWheel(req InstallRequest) ([]InstallTarget, error) {
	if len(req.Targets) == 0 {
		return nil, fmt.Errorf("skills: pip-wheel install needs at least one target")
	}
	staging := req.Targets[0].StagingDir
	if err := skills.ExtractAllFromBytes(req.ZipBytes, staging); err != nil {
		return nil, fmt.Errorf("skills: pip-wheel extract staging: %w", err)
	}
	wheelsDir := filepath.Join(staging, "wheels")
	if _, err := os.Stat(wheelsDir); err != nil {
		return nil, fmt.Errorf("skills: pip-wheel expects %q in the zip: %w", "wheels/", err)
	}
	uv, err := exec.LookPath("uv")
	if err != nil {
		return nil, fmt.Errorf("skills: pip-wheel requires `uv` on PATH (install with 'pip install uv' or 'brew install uv'): %w", err)
	}
	// Find the wheel file (caller may pin it in install_config.wheel;
	// otherwise we install whatever is in wheels/, which uv resolves
	// against the package metadata in the wheel itself).
	args := []string{"pip", "install", "--no-index", "--find-links", wheelsDir}
	if w, ok := req.Config["wheel"].(string); ok && w != "" {
		args = append(args, w)
	}
	cmd := exec.Command(uv, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("skills: pip-wheel install failed: %w", err)
	}
	return req.Targets[:1], nil
}

// installNpmPack installs an npm package globally using install_config.package.
// The zip payload is optional and ignored (npm fetches the registry itself).
//
// Only the first target is used; the install is system-wide.
func installNpmPack(req InstallRequest) ([]InstallTarget, error) {
	if len(req.Targets) == 0 {
		return nil, fmt.Errorf("skills: npm-pack install needs at least one target")
	}
	pkg, _ := req.Config["package"].(string)
	if pkg == "" {
		return nil, fmt.Errorf("skills: npm-pack requires install_config.package")
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return nil, fmt.Errorf("skills: npm-pack requires `npm` on PATH: %w", err)
	}
	cmd := exec.Command(npm, "install", "-g", pkg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("skills: npm install -g %q failed: %w", pkg, err)
	}
	return req.Targets[:1], nil
}

// installTarball extracts the zip to staging, then extracts the contained
// .tar.gz payload into the first target's FinalPath.
//
// The zip layout is assumed to be:
//
//	<staging>/payload.tar.gz    ← extracted to target.FinalPath
//	<staging>/SKILL.md          ← parsed at upload, not used here
//
// install_config.strip_components, when set to a positive int N, drops
// the first N leading path components of every entry (GNU tar --strip).
func installTarball(req InstallRequest) ([]InstallTarget, error) {
	if len(req.Targets) == 0 {
		return nil, fmt.Errorf("skills: tarball install needs at least one target")
	}
	staging := req.Targets[0].StagingDir
	if err := skills.ExtractAllFromBytes(req.ZipBytes, staging); err != nil {
		return nil, fmt.Errorf("skills: tarball extract staging: %w", err)
	}
	stripN := 0
	if s, ok := req.Config["strip_components"]; ok {
		switch v := s.(type) {
		case int:
			stripN = v
		case int64:
			stripN = int(v)
		case float64:
			stripN = int(v)
		}
	}
	payloadTar, err := findTarballPayload(staging)
	if err != nil {
		return nil, err
	}
	if err := extractTarballToDir(payloadTar, req.Targets[0].FinalPath, stripN); err != nil {
		return nil, fmt.Errorf("skills: tarball extract to %s: %w", req.Targets[0].FinalPath, err)
	}
	return req.Targets[:1], nil
}

// findTarballPayload looks for the only .tar.gz file in dir. Returns
// its full path. Errors if there's not exactly one.
func findTarballPayload(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("skills: read staging dir %s: %w", dir, err)
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tar.gz") || strings.HasSuffix(e.Name(), ".tgz") {
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	if len(found) == 0 {
		return "", fmt.Errorf("skills: tarball install needs a .tar.gz in the zip (got nothing in %s)", dir)
	}
	if len(found) > 1 {
		return "", fmt.Errorf("skills: tarball install expects exactly one .tar.gz in the zip; found %d", len(found))
	}
	return found[0], nil
}

// extractTarballToDir opens the .tar.gz at tarPath and writes every entry
// to destDir. zip-slip defense mirrors skills.ExtractAllFromBytes.
//
// stripComponents, when >0, drops that many leading path segments from
// each entry's name (GNU tar --strip-components). Equivalent to chdir'ing
// into the N-th subdir before extracting.
func extractTarballToDir(tarPath, destDir string, stripComponents int) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", tarPath, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar header: %w", err)
		}
		name := hdr.Name
		if stripComponents > 0 {
			parts := strings.Split(name, "/")
			if len(parts) <= stripComponents {
				continue // skip entries that become empty after stripping
			}
			name = strings.Join(parts[stripComponents:], "/")
		}
		target := filepath.Join(destDir, name)
		// zip-slip defense.
		rel, err := filepath.Rel(destDir, target)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("tar entry %q escapes destination", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := hdr.FileInfo().Mode().Perm()
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Symlinks are skipped (mirrors skills.ExtractAllFromBytes).
			continue
		default:
			// xattrs, hardlinks, char/block devices, fifos → skip.
			continue
		}
	}
	return nil
}

// ResolveTargets computes the InstallTarget list for a SkillVersion.
//
// Rules:
//  1. For each agent in sv.Agents (excluding "all"), pick the path:
//     sv.InstallPaths[agent] if present, else skills.DefaultAgentInstallPaths[agent].
//  2. If sv.Agents contains "all" or is empty, append skills.FallbackPath
//     (under "all" key).
//  3. Non-zip-extract methods only need one target (system-wide) — keep
//     the first entry and discard the rest.
//  4. Dedup by FinalPath so two agents pointing at the same path don't
//     cause double-install.
//  5. targetDirOverride (when non-empty) replaces FallbackPath AND the
//     default-per-agent paths — i.e. it's a global root for ALL targets.
//     This preserves the historical `--target-dir <dir>` CLI behavior
//     where the user expects all skill files under <dir> regardless of
//     which agent they target.
//
// stagingParent is the parent dir for per-target staging directories;
// callers typically pass t.TempDir() or os.TempDir(). One staging dir is
// created per target.
func ResolveTargets(sv *skills.SkillVersion, name, targetDirOverride, stagingParent string) ([]InstallTarget, error) {
	if name == "" {
		return nil, fmt.Errorf("skills: ResolveTargets: empty skill name")
	}
	if err := os.MkdirAll(stagingParent, 0o755); err != nil {
		return nil, fmt.Errorf("skills: mkdir staging parent: %w", err)
	}
	home, _ := os.UserHomeDir()
	expand := func(tpl string) string {
		out := strings.ReplaceAll(tpl, "$HOME", home)
		out = strings.ReplaceAll(out, "$NAME", name)
		return out
	}
	override := targetDirOverride != ""

	var raw []InstallTarget
	seenPath := map[string]bool{}
	add := func(agent, tpl string) {
		var final string
		if override {
			final = filepath.Join(targetDirOverride, name)
		} else {
			final = expand(tpl)
		}
		if seenPath[final] {
			return
		}
		seenPath[final] = true
		raw = append(raw, InstallTarget{
			Agent:     agent,
			FinalPath: final,
		})
	}

	// Agents is required by Validate; empty is defensive only.
	for _, a := range sv.Agents {
		if a == "all" {
			// Universal sentinel — honour the author's explicit
			// `install_paths.all` override (validators now allow it)
			// and fall back to the central FallbackPath when unset.
			tpl := skills.FallbackPath
			if override, ok := sv.InstallPaths["all"]; ok && override != "" {
				tpl = override
			}
			add("all", tpl)
			continue
		}
		if tpl, ok := sv.InstallPaths[a]; ok && tpl != "" {
			add(a, tpl)
			continue
		}
		if tpl, ok := skills.DefaultAgentInstallPaths[a]; ok {
			add(a, tpl)
			continue
		}
		// agents[] was validated server-side, so we should never land here.
		return nil, fmt.Errorf("skills: agent %q has no default install path and no override in install_paths", a)
	}
	if len(raw) == 0 {
		// Defensive: empty Agents → fall back to central.
		add("all", skills.FallbackPath)
	}

	// Non-zip-extract methods are system-wide; keep the first target.
	if sv.InstallMethod != methodZipExtract && sv.InstallMethod != "" && len(raw) > 1 {
		raw = raw[:1]
	}

	// Create per-target staging dirs.
	for i := range raw {
		dir, err := os.MkdirTemp(stagingParent, fmt.Sprintf(".skill-staging-%s-*", raw[i].Agent))
		if err != nil {
			// Best-effort cleanup of the staging dirs we already made.
			for j := 0; j < i; j++ {
				_ = os.RemoveAll(raw[j].StagingDir)
			}
			return nil, fmt.Errorf("skills: create staging for %s: %w", raw[i].Agent, err)
		}
		raw[i].StagingDir = dir
	}
	return raw, nil
}

// CleanupStaging removes the per-target staging directories created by
// ResolveTargets. Safe to call even if Install never ran. Errors are
// swallowed (best-effort).
func CleanupStaging(targets []InstallTarget) {
	for _, t := range targets {
		if t.StagingDir != "" {
			_ = os.RemoveAll(t.StagingDir)
		}
	}
}

// Ensure import-only references stay used (mirrors zip.go's pattern).
var _ = fmt.Sprintf
