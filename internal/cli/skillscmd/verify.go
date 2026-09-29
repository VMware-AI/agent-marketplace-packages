package skillscmd

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/spf13/cobra"
)

// NewSkillsVerifyCmd creates `agentpkg skills verify`.
//
// Validates the SKILL.md frontmatter at the given path (or path/SKILL.md
// if a directory was passed). Prints a human-readable summary including
// body length, detected metadata inputs, and any validation warnings.
func NewSkillsVerifyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "verify <path>",
		Short: "Parse SKILL.md frontmatter, validate against the schema, print a summary",
		Long: `verify parses a SKILL.md (or <path>/SKILL.md if path is a directory),
runs schema validation, and prints a human-readable summary. Exit 0 on
success, non-zero on validation errors.

Unlike ` + "`agentpkg package verify`" + ` (which also recomputes payload checksums
against manifest.json), skills verify does NOT have any "payload" to
verify — the SKILL.md is the only required file inside the zip.

<path> may be a directory containing SKILL.md, a SKILL.md file, or a
.zip archive with SKILL.md at its root.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return verifySkill(args[0], cmd.OutOrStdout())
		},
	}
	return c
}

// verifySkill is the actual verification logic. Reusable from tests.
// Output goes to out so tests can capture it; production callers pass
// cmd.OutOrStdout().
func verifySkill(path string, out io.Writer) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	var skillMD string
	switch {
	case fi.IsDir():
		skillMD = filepath.Join(path, "SKILL.md")
		if _, err := os.Stat(skillMD); err != nil {
			return fmt.Errorf("no SKILL.md found at %s", skillMD)
		}
	case strings.EqualFold(filepath.Ext(path), ".zip"):
		// F023 (tested 2026-09-30): zip-path used to be passed straight to
		// skills.LoadSkillFile, which read the zip header bytes as markdown
		// and emitted "must start with ---" regardless of zip contents.
		// Extract SKILL.md from the zip to a tmpfile (preserves ZipFile
		// semantics; symlinks/permissions inside the zip don't matter for
		// frontmatter validation).
		extracted, err := extractSkillMDFromZip(path)
		if err != nil {
			fmt.Fprintf(out, "  SKILL.md:  FAIL  %v\n", err)
			return err
		}
		defer os.Remove(extracted)
		skillMD = extracted
	default:
		skillMD = path
	}
	fmt.Fprintf(out, "Verifying %s ...\n", skillMD)
	m, err := skills.LoadSkillFile(skillMD)
	if err != nil {
		fmt.Fprintf(out, "  SKILL.md:  FAIL  %v\n", err)
		return err
	}
	m.Defaults()
	fmt.Fprintf(out, "  name:        %s\n", m.Name)
	fmt.Fprintf(out, "  version:     %s\n", m.Version)
	if m.Author != "" {
		fmt.Fprintf(out, "  author:      %s\n", m.Author)
	}
	fmt.Fprintf(out, "  description: %d chars\n", len(m.Description))
	fmt.Fprintf(out, "  body:        %d chars\n", len(m.Body))
	if len(m.Tags) > 0 {
		fmt.Fprintf(out, "  tags:        %v\n", m.Tags)
	} else {
		fmt.Fprintf(out, "  tags:        (none)\n")
	}
	if n := len(m.Metadata.Inputs); n > 0 {
		fmt.Fprintf(out, "  inputs:      %d field(s)\n", n)
		for _, in := range m.Metadata.Inputs {
			req := ""
			if in.Required {
				req = " (required)"
			}
			fmt.Fprintf(out, "    - %s : %s%s\n", in.Name, in.Type, req)
		}
	}
	if m.Metadata.EntryPoint != "" {
		fmt.Fprintf(out, "  entry_point: %s\n", m.Metadata.EntryPoint)
	}
	fmt.Fprintln(out, "  SKILL.md:  ok")
	return nil
}

// extractSkillMDFromZip reads SKILL.md from a .zip archive to a tmpfile.
// Looks at the root of the zip first; if the author packaged it under
// a top-level prefix (e.g. "my-skill/SKILL.md"), uses the first match.
// Returns the tmpfile path; caller must os.Remove it.
func extractSkillMDFromZip(zipPath string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var skillMD *zip.File
	for _, f := range r.File {
		if f.Name == "SKILL.md" || strings.HasSuffix(f.Name, "/SKILL.md") {
			skillMD = f
			break
		}
	}
	if skillMD == nil {
		return "", fmt.Errorf("no SKILL.md in zip %s", zipPath)
	}
	fh, err := skillMD.Open()
	if err != nil {
		return "", fmt.Errorf("open %s in zip: %w", skillMD.Name, err)
	}
	defer fh.Close()
	data, err := io.ReadAll(fh)
	if err != nil {
		return "", fmt.Errorf("read %s in zip: %w", skillMD.Name, err)
	}
	tmp, err := os.CreateTemp("", "agentpkg-skill-md-*.md")
	if err != nil {
		return "", fmt.Errorf("create tmp for SKILL.md: %w", err)
	}
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("write tmp SKILL.md: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("close tmp SKILL.md: %w", err)
	}
	return tmp.Name(), nil
}
