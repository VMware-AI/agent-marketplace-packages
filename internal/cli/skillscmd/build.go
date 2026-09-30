package skillscmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/spf13/cobra"
)

// strictSemverCLIRe mirrors internal/skills/manifest.go's strictSemverRE
// (semver.IsValid is too lenient — accepts "1" and "1.0" as canonical
// for "1.0.0"). We don't import the regex from the skills package
// because it's unexported; duplicating a 1-line regex is cheaper than
// exporting one.
var strictSemverCLIRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z\-]+(?:\.[0-9A-Za-z\-]+)*)?(?:\+[0-9A-Za-z\-]+(?:\.[0-9A-Za-z\-]+)*)?$`)

// NewSkillsBuildCmd creates `agentpkg skills build`.
//
// Packages a directory containing SKILL.md + supporting files into
// dist/skills/<name>-<source>-<version>.zip, writes the .sha256 sidecar,
// and refreshes dist/skills-index.json. Mirrors `agentpkg package build`
// for the agent layout, but produces ZIPs (not tar.gz) and uses SKILL.md
// (not manifest.json + meta.yaml) as the manifest.
func NewSkillsBuildCmd() *cobra.Command {
	var (
		version string
		source  string
		channel string
		outDir  string
		dryRun  bool
	)
	c := &cobra.Command{
		Use:   "build <dir> --version X.Y.Z [--source community] [--channel stable] [--out dist]",
		Short: "Package one version of a skill into dist/skills/<name>-<source>-<version>.zip + .sha256 and refresh skills-index.json",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if version == "" {
				return fmt.Errorf("--version is required (strict semver MAJOR.MINOR.PATCH)")
			}
			if source == "" {
				source = "community"
			}
			if !skills.IsValidSource(source) {
				return fmt.Errorf("--source %q is invalid (want: community, internal)", source)
			}
			if channel == "" {
				channel = "stable"
			}
			if outDir == "" {
				outDir = "dist"
			}
			return buildSkill(args[0], source, channel, version, outDir, dryRun, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&version, "version", "", "strict semver version segment (required, e.g. 1.0.0)")
	c.Flags().StringVar(&source, "source", "community", "source tree (community | internal)")
	c.Flags().StringVar(&channel, "channel", "stable", "channel (stable | beta | edge | internal)")
	c.Flags().StringVar(&outDir, "out", "dist", "output directory (dist/skills/ is created under it)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "build the zip in a temp dir but don't write to dist/")
	return c
}

// buildSkill packages dir into outDir/skills/<name>-<source>-<version>.zip.
// The dir name MUST match the skill's name in SKILL.md (mismatch is a
// hard error to catch copy-paste mistakes).
func buildSkill(dir, source, channel, version, outDir string, dryRun bool, out io.Writer) error {
	if !strictSemverCLIRe.MatchString(version) {
		return fmt.Errorf("--version %q is not strict semver (want MAJOR.MINOR.PATCH[-prerelease])", version)
	}
	skillMDPath := filepath.Join(dir, "SKILL.md")
	m, err := skills.LoadSkillFile(skillMDPath)
	if err != nil {
		return fmt.Errorf("parse SKILL.md: %w", err)
	}
	m.Defaults()
	dirName := filepath.Base(dir)
	if m.Name != dirName {
		return fmt.Errorf("directory name %q does not match SKILL.md name %q — must match for registry identity", dirName, m.Name)
	}

	if dryRun {
		// Skip MkdirAll in dry-run so the filesystem stays untouched.
		zipName := fmt.Sprintf("%s-%s-%s.zip", m.Name, source, version)
		fmt.Fprintf(out, "(dry-run) would write %s/skills/%s\n", outDir, zipName)
		fmt.Fprintf(out, "(dry-run) would refresh %s/skills-index.json\n", outDir)
		return nil
	}

	skillsDir := filepath.Join(outDir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", skillsDir, err)
	}

	tmpDir, err := os.MkdirTemp(skillsDir, ".build-*")
	if err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	tmpZip := filepath.Join(tmpDir, "out.zip")
	if _, err := skills.BuildFromDir(dir, tmpZip); err != nil {
		return fmt.Errorf("build zip: %w", err)
	}

	zipName := fmt.Sprintf("%s-%s-%s.zip", m.Name, source, version)
	finalZip := filepath.Join(skillsDir, zipName)
	if err := os.Rename(tmpZip, finalZip); err != nil {
		return fmt.Errorf("rename zip: %w", err)
	}

	hex, err := sha256File(finalZip)
	if err != nil {
		return fmt.Errorf("sha256: %w", err)
	}
	sidecar := finalZip + ".sha256"
	tmpSidecar := sidecar + ".tmp"
	if err := os.WriteFile(tmpSidecar, []byte(hex+"  "+zipName+"\n"), 0o644); err != nil {
		return fmt.Errorf("write sidecar: %w", err)
	}
	if err := os.Rename(tmpSidecar, sidecar); err != nil {
		return fmt.Errorf("rename sidecar: %w", err)
	}

	fmt.Fprintf(out, "Built %s (%s)\n", finalZip, humanSize(fileSize(finalZip)))
	fmt.Fprintf(out, "  sha256: %s\n", hex)

	if err := reindexSkills(outDir, false, out); err != nil {
		return fmt.Errorf("reindex: %w", err)
	}
	return nil
}

// reindexSkills scans every .zip in outDir/skills/, extracts SKILL.md
// from each, and writes outDir/skills-index.json.
//
// The index is rebuilt from scratch every time (not incrementally
// updated) — this is consistent with how the agent reindex works and
// avoids drift if a previous build left orphan entries. Cost is small:
// one open+extract per zip, linear in the number of published skills.
func reindexSkills(outDir string, dryRun bool, out io.Writer) error {
	dist := filepath.Join(outDir, "skills")
	zipNames, err := zipNamesInDist(dist)
	if err != nil {
		return fmt.Errorf("scan %s: %w", dist, err)
	}
	idx := &skills.Index{}
	idx.Schema = skills.IndexSchemaVersion
	idx.GeneratedAt = nowUTC()
	for _, zipName := range zipNames {
		zipPath := filepath.Join(dist, zipName)
		m, raw, err := skills.ExtractSkillMD(zipPath)
		if err != nil {
			fmt.Fprintf(out, "WARN: skipping %s: %v\n", zipName, err)
			continue
		}
		name, source, version, perr := parseSkillZipName(zipName)
		if perr != nil {
			fmt.Fprintf(out, "WARN: cannot parse zip filename %s: %v\n", zipName, perr)
			continue
		}
		// ParseZipFilename now returns empty values for unrecognised
		// layouts (best-effort) rather than erroring. CLI build needs
		// a complete (name, source, version) tuple to write to the
		// index, so skip zips whose identity can't be derived from
		// the filename — the operator should rename it and rerun.
		if name == "" || version == "" {
			fmt.Fprintf(out, "WARN: cannot derive identity from %s (missing name/version, rename and rerun)\n", zipName)
			continue
		}
		if m.Name != name || m.Version != version {
			fmt.Fprintf(out,
				"WARN: %s: SKILL.md says name=%q version=%q but filename says %q/%q — using filename as identity\n",
				zipName, m.Name, m.Version, name, version)
		}
		hex, err := sha256File(zipPath)
		if err != nil {
			fmt.Fprintf(out, "WARN: cannot hash %s: %v\n", zipPath, err)
			continue
		}
		_ = m
		sv := skills.SkillVersion{
			Version:    version,
			Source:     source,
			Channel:    "stable",
			ReleasedAt: nowUTC(),
			Zip: skills.Zip{
				Filename:  zipName,
				SizeBytes: fileSize(zipPath),
				SHA256:    "sha256:" + hex,
			},
			Body:       string(raw),
			Requires:   &m.Metadata.Requires,
			Inputs:     m.Metadata.Inputs,
			EntryPoint: m.Metadata.EntryPoint,
		}
		var found *skills.Skill
		for i := range idx.Skills {
			if idx.Skills[i].Name == name {
				found = &idx.Skills[i]
				break
			}
		}
		if found == nil {
			idx.Skills = append(idx.Skills, skills.Skill{
				Name:        name,
				DisplayName: titleCase(name),
				Description: m.Description,
				Author:      m.Author,
				Tags:        m.Tags,
				License:     m.License,
				Homepage:    m.Homepage,
				CreatedAt:   m.CreatedAt,
				Versions:    []skills.SkillVersion{sv},
			})
		} else {
			found.Versions = append(found.Versions, sv)
		}
	}
	for i := range idx.Skills {
		idx.Skills[i].SortVersions()
	}

	outPath := filepath.Join(outDir, "skills-index.json")
	if dryRun {
		fmt.Fprintf(out, "(dry-run) would write %s with %d skill(s)\n", outPath, len(idx.Skills))
		return nil
	}
	data, err := skills.MarshalIndex(idx)
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	if err := writeFileAtomic(outPath, data, 0o644); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	fmt.Fprintf(out, "Wrote %s (%d skill(s))\n", outPath, len(idx.Skills))
	return nil
}

// zipNamesInDist lists *.zip files in dist (sorted). Directory entries
// (.tmp/ scaffolding) are ignored.
func zipNamesInDist(dist string) ([]string, error) {
	entries, err := os.ReadDir(dist)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".zip") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// nowUTC returns the current UTC time in RFC3339.
func nowUTC() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}