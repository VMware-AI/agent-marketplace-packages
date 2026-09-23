package skillscmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

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
verify — the SKILL.md is the only required file inside the zip.`,
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
	skillMD := path
	if fi.IsDir() {
		skillMD = filepath.Join(path, "SKILL.md")
		if _, err := os.Stat(skillMD); err != nil {
			return fmt.Errorf("no SKILL.md found at %s", skillMD)
		}
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
