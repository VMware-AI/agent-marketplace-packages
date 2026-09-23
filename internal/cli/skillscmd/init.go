package skillscmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// skillMDTemplate is the starter SKILL.md scaffolded by
// `agentpkg skills init`. Anthropic Skills compatible (only `name` and
// `description` are required); registry extensions live at the top
// level under our own keys so Anthropic clients can ignore them.
//
// Substitution tokens: %NAME%, %TITLE%, %AUTHOR%, %DATE%. We use a
// plain string-replace instead of text/template because there are only
// four tokens and the failure mode of a broken template at CLI init time
// is worse than a literal substring search.
const skillMDTemplate = `---
# Anthropic-compatible required fields.
name: %NAME%
description: Short description for %NAME% (10-200 chars). What does this skill do and when should it be used?

# Anthropic-compatible optional fields.
license: MIT
compatibility: Requires Python 3.10+, jq

# Registry extensions (Anthropic ignores unknown keys).
version: 0.1.0
author: %AUTHOR%
category: %CATEGORY%
agents: [%AGENTS%]
tags: []
created_at: %DATE%

# Registry extensions grouped under the Anthropic-allowed 'metadata' key.
metadata:
  requires:
    os: [linux, darwin]
    arch: [amd64, arm64]
    tools: [bash]
  inputs:
    - name: example_input
      description: One example input field
      required: false
      type: string
      default: ""
  entry_point: scripts/run.sh
  install_method: %INSTALL_METHOD%
---

# %TITLE%

Long-form Markdown describing this skill. This body is what consumers see
when they click the skill in the marketplace UI.

## Usage
Describe how an agent should use this skill.

## Examples
Show concrete invocations.

## Notes
Anything else worth mentioning.
`

// skillsTreeTemplate documents the layout scaffolded by `skills init`.
// Currently unused at runtime; kept as a printable reference.
const skillsTreeTemplate = `skills/<name>/
├── SKILL.md          # required: YAML frontmatter + Markdown body
├── scripts/          # optional: executable scripts (preserves +x in zip)
├── references/       # optional: long-form docs loaded on demand
├── assets/           # optional: templates, images, data files
└── workflows/        # optional: step-by-step guides
`

// NewSkillsInitCmd creates `agentpkg skills init`.
func NewSkillsInitCmd() *cobra.Command {
	var (
		dir          string
		author       string
		category     string
		agents       string
		installMethod string
		force        bool
	)
	c := &cobra.Command{
		Use:   "init <name>",
		Short: "Scaffold a new skill directory with a starter SKILL.md (Anthropic Skills compatible)",
		Long: `init creates skills/<name>/ with an Anthropic-compatible SKILL.md
template and the recommended sub-directories (scripts/, references/,
assets/, workflows/). Use --author to pre-fill the author field, or
leave the placeholder for the user to edit later.

The scaffolded SKILL.md is intentionally minimal (name + description +
registry placeholders). Use 'agentpkg skills verify' to validate before
'agentpkg skills build'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			root := dir
			if root == "" {
				root = defaultSkillsDir(name)
			}
			return scaffoldSkill(name, root, scaffoldParams{
				Author:        author,
				Category:      category,
				Agents:        agents,
				InstallMethod: installMethod,
				Force:         force,
			})
		},
	}
	c.Flags().StringVar(&dir, "dir", "", "output root directory (default: ./skills/<name>)")
	c.Flags().StringVar(&author, "author", "", "pre-fill the author: field in SKILL.md (default: empty)")
	c.Flags().StringVar(&category, "category", "other", "pre-fill the category: field (default: other)")
	c.Flags().StringVar(&agents, "agents", "all", "comma-separated agents: field (default: all)")
	c.Flags().StringVar(&installMethod, "install-method", "zip-extract", "pre-fill metadata.install_method (default: zip-extract)")
	c.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	return c
}

// scaffoldParams bundles the cobra flag values for scaffoldSkill. Pulled
// out as a struct so test code can call scaffoldSkill directly without
// rebuilding cobra plumbing.
type scaffoldParams struct {
	Author        string
	Category      string
	Agents        string
	InstallMethod string
	Force         bool
}

// scaffoldSkill writes the SKILL.md and creates the recommended sub-dirs
// under root. Refuses to overwrite an existing root unless --force is set.
func scaffoldSkill(name, root string, p scaffoldParams) error {
	if _, err := os.Stat(root); err == nil && !p.Force {
		return fmt.Errorf("directory %s already exists; use --force to overwrite", root)
	}
	body := renderSkillMD(name, p)
	full := filepath.Join(root, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", full, err)
	}
	fmt.Println("  +", full)
	// Empty sub-dirs with .gitkeep so the layout shows up in `git status`.
	for _, sub := range []string{"scripts", "references", "assets", "workflows"} {
		dir := filepath.Join(root, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		gitkeep := filepath.Join(dir, ".gitkeep")
		if err := os.WriteFile(gitkeep, []byte(""), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", gitkeep, err)
		}
	}
	fmt.Println()
	fmt.Printf("Scaffolded %s\n", root)
	fmt.Println("Next steps:")
	fmt.Println("  1. edit", filepath.Join(root, "SKILL.md"), "— fill in description, version, author")
	fmt.Println("  2. drop entry-point into", filepath.Join(root, "scripts/"))
	fmt.Println("  3. agentpkg skills verify " + root)
	fmt.Println("  4. agentpkg skills build " + root + " --version 0.1.0")
	return nil
}

// renderSkillMD substitutes placeholders into skillMDTemplate. Plain
// strings.ReplaceAll — no template engine needed for the four required
// tokens (plus the optional v2.0 fields).
func renderSkillMD(name string, p scaffoldParams) string {
	out := skillMDTemplate
	out = strings.ReplaceAll(out, "%NAME%", name)
	out = strings.ReplaceAll(out, "%TITLE%", titleCase(name))
	out = strings.ReplaceAll(out, "%AUTHOR%", p.Author)
	out = strings.ReplaceAll(out, "%CATEGORY%", defaultStr(p.Category, "other"))
	out = strings.ReplaceAll(out, "%AGENTS%", defaultStr(p.Agents, "all"))
	out = strings.ReplaceAll(out, "%INSTALL_METHOD%", defaultStr(p.InstallMethod, "zip-extract"))
	out = strings.ReplaceAll(out, "%DATE%", time.Now().UTC().Format("2006-01-02"))
	return out
}

// defaultStr returns def when s is empty. Tiny helper so callers can pass
// zero-value CLI strings without nil-checking.
func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// titleCase returns s with the first letter capitalized and the rest
// untouched (so "web-search" → "Web-search", close enough for a scaffold
// title without dragging in cases.Title).
func titleCase(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 32
	}
	return string(b)
}