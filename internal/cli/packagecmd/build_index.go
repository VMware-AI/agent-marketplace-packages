package packagecmd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/VMware-AI/agent-marketplace-packages/internal/manifest"
	"gopkg.in/yaml.v3"
)

// buildIndexFromDir scans dist/ for tarballs and assembles an apitypes.Index
// where each (name, source) gets one Agent entry with multiple Versions.
//
// For each tarball we extract manifest.json (required) and meta.yaml
// (optional, embedded by `package build`). The agent-level entry inherits
// its marketing fields from meta.yaml.
func buildIndexFromDir(outDir string) (*apitypes.Index, error) {
	idx := &apitypes.Index{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Schema:      apitypes.SchemaVersion,
	}

	// Walk every *.tar.gz in dist/, sorted.
	entries, err := filepath.Glob(filepath.Join(outDir, "*.tar.gz"))
	if err != nil {
		return nil, fmt.Errorf("glob dist: %w", err)
	}
	sort.Strings(entries)

	// agentEntries[agentKey] = *apitypes.Agent
	// agentKey = "<name>|<source>"
	type agentKey = string
	agentEntries := map[agentKey]*apitypes.Agent{}

	for _, tarPath := range entries {
		tarName := filepath.Base(tarPath)

		files, err := extractTarballFiles(tarPath, "manifest.json", "meta.yaml")
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", tarName, err)
			continue
		}
		manifestData := files["manifest.json"]
		metaData := files["meta.yaml"]
		if manifestData == nil {
			// Fallback: manifest might be nested under <version>/manifest.json
			// (legacy layout) — re-scan the whole tarball for any file named
			// "manifest.json" and pick the first.
			manifestData, err = findFileInTarball(tarPath, "manifest.json")
			if err != nil {
				fmt.Fprintf(os.Stderr, "skipping %s: no manifest.json (%v)\n", tarName, err)
				continue
			}
		}
		m, err := manifest.LoadFromBytes(manifestData)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", tarName, err)
			continue
		}

		// Read .sha256 sidecar.
		expectedHex, err := readSHA256Sidecar(tarPath + ".sha256")
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", tarName, err)
			continue
		}

		// Build the Version entry.
		v := apitypes.Version{
			Version:    m.Version,
			Source:     m.Source,
			Channel:    m.Channel,
			ReleasedAt: m.ReleasedAt,
			Tarball: apitypes.Tarball{
				Filename:  tarName,
				SizeBytes: statSize(tarPath),
				SHA256:    "sha256:" + expectedHex,
			},
			Manifest: convertManifest(m),
		}

		// Aggregate into the per-(name,source) Agent entry.
		// Name comes from the manifest, NOT from splitting the tarball
		// filename — CalVer-style versions like "2026.7.1-2" contain
		// dashes that would split the name incorrectly.
		key := m.Agent + "|" + m.Source
		parsed := parsedName{name: m.Agent, source: m.Source, version: m.Version}
		agent, ok := agentEntries[key]
		if !ok {
			agent = &apitypes.Agent{
				Name:     parsed.name,
				Tags:     []string{},
				Versions: []apitypes.Version{},
			}
			// Apply meta.yaml if present.
			if metaData != nil {
				var meta manifest.Meta
				if err := yaml.Unmarshal(metaData, &meta); err == nil {
					meta.Defaults(parsed.name)
					agent.DisplayName = meta.DisplayName
					agent.Description = meta.Description
					agent.Icon = meta.Icon
					agent.Category = meta.Category
					agent.Tags = meta.Tags
				}
			}
			agentEntries[key] = agent
		}
		// First meta wins; ignore subsequent meta.yaml in different versions
		// (meta.yaml should be identical across versions of the same agent,
		// but if they differ we keep the first one and log a warning).
		agent.Versions = append(agent.Versions, v)
	}

	// Stable order: by agent name.
	for _, a := range agentEntries {
		// Sort versions: newest first by semver-ish string compare.
		sort.SliceStable(a.Versions, func(i, j int) bool {
			return a.Versions[i].Version > a.Versions[j].Version
		})
		idx.Agents = append(idx.Agents, *a)
	}
	sort.Slice(idx.Agents, func(i, j int) bool {
		return idx.Agents[i].Name < idx.Agents[j].Name
	})

	return idx, nil
}

// parsedName is the (name, source, version) extracted from a tarball filename.
type parsedName struct {
	name    string
	source  string
	version string
}

// parseTarballName splits "opencode-upstream-0.0.55.tar.gz" into its parts.
//
// Format: <name>-<source>-<version>.tar.gz where version is the LAST
// dash-segment (so a CalVer-like "2026.7.2" works fine).
func parseTarballName(filename string) (parsedName, error) {
	if !strings.HasSuffix(filename, ".tar.gz") {
		return parsedName{}, fmt.Errorf("not a tar.gz: %s", filename)
	}
	stem := strings.TrimSuffix(filename, ".tar.gz")
	parts := strings.Split(stem, "-")
	if len(parts) < 3 {
		return parsedName{}, fmt.Errorf("expected <name>-<source>-<version>.tar.gz, got %s", filename)
	}
	version := parts[len(parts)-1]
	source := parts[len(parts)-2]
	name := strings.Join(parts[:len(parts)-2], "-")
	if name == "" || source == "" || version == "" {
		return parsedName{}, fmt.Errorf("empty component in %s", filename)
	}
	return parsedName{name: name, source: source, version: version}, nil
}

// extractTarballFiles extracts two specific files from a tar.gz, returning
// their bytes. Either may be nil if not present.
func extractTarballFiles(tarPath string, names ...string) (map[string][]byte, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if want[hdr.Name] {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			out[hdr.Name] = data
		}
	}
	return out, nil
}

// readSHA256Sidecar reads the first whitespace-separated token (the hex
// digest) from a .sha256 sidecar file.
func readSHA256Sidecar(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty sha256 sidecar: %s", path)
	}
	return fields[0], nil
}

// convertManifest maps internal/manifest.Manifest → apitypes.Manifest.
// Both have the same field set but live in different packages to keep the
// API surface independent.
func convertManifest(m *manifest.Manifest) apitypes.Manifest {
	out := apitypes.Manifest{
		SchemaVersion: m.SchemaVersion,
		Agent:         m.Agent,
		Source:        m.Source,
		Version:       m.Version,
		Channel:       m.Channel,
		ReleasedAt:    m.ReleasedAt,
		Checksums:     map[string]string{},
	}
	if m.Upstream != nil {
		out.Upstream = &apitypes.Upstream{
			Name: m.Upstream.Name, Version: m.Upstream.Version,
			URL: m.Upstream.URL, SHA256: m.Upstream.SHA256, Notes: m.Upstream.Notes,
		}
	}
	if m.ForkOf != nil {
		out.ForkOf = &apitypes.ForkOf{
			Agent: m.ForkOf.Agent, Version: m.ForkOf.Version, Source: m.ForkOf.Source,
		}
	}
	if m.Requires != nil {
		out.Requires = &apitypes.Requires{
			OS: m.Requires.OS, Arch: m.Requires.Arch,
			SystemPackages: m.Requires.SystemPackages,
			SystemTools:    m.Requires.SystemTools,
		}
	}
	for _, rc := range m.RuntimeConstraints {
		out.RuntimeConstraints = append(out.RuntimeConstraints, apitypes.RuntimeConstraint{
			Name: rc.Name, MinVersion: rc.MinVersion, MaxVersion: rc.MaxVersion,
			UseSystem: rc.UseSystem,
		})
	}
	for _, pe := range m.Payload {
		out.Payload = append(out.Payload, apitypes.PayloadEntry{Src: pe.Src, Dst: pe.Dst, Mode: pe.Mode})
	}
	for k, v := range m.Checksums {
		out.Checksums[k] = v
	}
	if m.Tarball != nil {
		out.Tarball = &apitypes.TarballRef{Filename: m.Tarball.Filename, SHA256: m.Tarball.SHA256}
	}
	if m.Upgrade != nil {
		out.Upgrade = &apitypes.Upgrade{
			Strategy: m.Upgrade.Strategy, CompatibleFrom: m.Upgrade.CompatibleFrom,
		}
		for _, me := range m.Upgrade.Migrations {
			out.Upgrade.Migrations = append(out.Upgrade.Migrations, apitypes.MigrationEntry{From: me.From, Script: me.Script})
		}
	}
	if m.Scripts != nil {
		out.Scripts = &apitypes.Scripts{Install: m.Scripts.Install, Uninstall: m.Scripts.Uninstall}
	}
	for _, s := range m.Services {
		out.Services = append(out.Services, apitypes.ServiceSpec{
			Name:        s.Name,
			Command:     append([]string(nil), s.Command...),
			Args:        append([]string(nil), s.Args...),
			Restart:     s.Restart,
			WorkingDir:  s.WorkingDir,
			Description: s.Description,
		})
	}
	for _, c := range m.Configs {
		out.Configs = append(out.Configs, apitypes.ConfigSpec{
			Name: c.Name, File: c.File, RenderTo: c.RenderTo, Mode: c.Mode,
		})
	}
	return out
}

// findFileInTarball scans the entire tarball and returns the contents of
// the FIRST regular file whose base name matches `wantBase` (e.g.
// "manifest.json"). Used as a fallback for legacy tarballs that nest
// manifest.json under a version segment.
//
// Returns os.ErrNotExist if no matching file is found.
func findFileInTarball(tarPath, wantBase string) ([]byte, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) == wantBase {
			return io.ReadAll(tr)
		}
	}
	return nil, os.ErrNotExist
}
