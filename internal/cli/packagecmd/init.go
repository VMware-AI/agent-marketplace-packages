// Package package implements the agentpkg package <subcommand> group.
//
// These commands are used by package authors — they scaffold, validate,
// build, sign, and reindex agent bundles. They all share the agents/<name>/
// on-disk layout.
package packagecmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/spf13/cobra"
)

// agentTemplate is the directory layout scaffolded by `package init`.
const agentTemplate = `agents/<name>/
├── meta.yaml                    # agent-level marketing metadata (display_name, description, logo, category, tags)
└── <source>/                    # upstream | ours
    └── <version>/               # semver (or CalVer) version segment
        ├── manifest.json         # technical manifest (schema_version, runtime_constraints, payload, ...)
        ├── install.sh            # ~70 line installer (system runtime + offline wheels)
        ├── uninstall.sh          # inverse of install.sh
        ├── payload/
        │   └── bin/<name>        # entry point
        ├── migrate/              # cross-version migration scripts
        │   └── from-X.Y.Z.sh
        └── README.md             # version-specific notes
`

// metaTemplate is the boilerplate for meta.yaml.
const metaTemplate = `# agents/<name>/meta.yaml
#
# Agent-level marketing metadata. This file is version-independent:
# all versions of <name> share the same meta.yaml. agentpkg package build
# embeds a copy into every tarball (as the top-level meta.yaml file) and
# also flattens the merged form into dist/index.json so the marketplace
# frontend can render cards without parsing tarballs.

# Required: human-readable name shown in the UI.
# Falls back to the agent name if left blank.
display_name: "<display_name>"

# Required: one-line description (10–200 chars). Shown on the card.
description: "<description>"

# Optional: logo. Leave empty to use the consumer's default placeholder.
# Accepts an http(s) URL or a data:image/<mime>;base64,... URL (preferred
# for offline / air-gapped deployments). See docs/logo-format.md.
logo: "<logo>"

# Required: one of the category IDs from docs/category-catalog.md.
category: "<category>"

# Optional: up to 10 search tags, lowercase, dash-separated.
tags:
  - tag-1
  - tag-2
`

// NewInitCmd creates `agentpkg package init`.
func NewInitCmd() *cobra.Command {
	var (
		name    string
		baseDir string
	)
	c := &cobra.Command{
		Use:   "init <name>",
		Short: "Scaffold a new agent package directory (meta.yaml + manifest.json + install.sh templates)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name = args[0]
			root := baseDir
			if root == "" {
				root = "agents/" + name
			}
			return scaffoldAgent(name, root)
		},
	}
	c.Flags().StringVar(&baseDir, "dir", "", "output root directory (default: ./agents/<name>)")
	return c
}

// scaffoldAgent writes the meta.yaml + first version directory tree
// under root. It does NOT overwrite existing files.
func scaffoldAgent(name, root string) error {
	if _, err := os.Stat(root); err == nil {
		return fmt.Errorf("directory %s already exists; refusing to overwrite", root)
	}

	files := map[string]string{
		"meta.yaml":                           renderMeta(name),
		"upstream/0.1.0/manifest.json":        renderManifest(name, "upstream", "0.1.0"),
		"upstream/0.1.0/install.sh":           renderInstallSh(),
		"upstream/0.1.0/uninstall.sh":         renderUninstallSh(),
		"upstream/0.1.0/README.md":            renderReadme(name),
		"upstream/0.1.0/payload/bin/.gitkeep": "# entry point goes here\n",
		"upstream/0.1.0/migrate/.gitkeep":     "# cross-version migration scripts\n",
	}

	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			return fmt.Errorf("write %s: %w", full, err)
		}
		fmt.Println("  +", full)
	}
	fmt.Println()
	fmt.Printf("Scaffolded %s\n", root)
	fmt.Println("Next steps:")
	fmt.Println("  1. edit agents/" + name + "/meta.yaml — fill in display_name/description/logo/category/tags")
	fmt.Println("  2. edit agents/" + name + "/upstream/0.1.0/manifest.json — set runtime_constraints, payload, requires")
	fmt.Println("  3. drop entry-point into agents/" + name + "/upstream/0.1.0/payload/bin/")
	fmt.Println("  4. agentpkg package verify agents/" + name)
	fmt.Println("  5. agentpkg package build agents/" + name + " --version 0.1.0")
	return nil
}

// renderMeta produces a starter meta.yaml for the given agent name.
func renderMeta(name string) string {
	tmpl, err := template.New("meta").Parse(metaTemplate)
	if err != nil {
		return metaTemplate // fall back to literal
	}
	data := map[string]string{
		"name":         name,
		"display_name": name,
		"description":  fmt.Sprintf("Short description for %s (10-200 chars).", name),
		"logo":         "",
		"category":     "utility",
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return metaTemplate
	}
	return buf.String()
}

func renderManifest(name, source, version string) string {
	return fmt.Sprintf(`{
  "schema_version": "1.1",
  "agent":         %q,
  "source":        %q,
  "version":       %q,
  "channel":       "stable",
  "released_at":   "2026-01-01",
  "requires": {
    "os":              ["linux"],
    "arch":            ["x86_64"],
    "system_packages": [],
    "system_tools":    ["tar", "sha256sum", "bash"]
  },
  "runtime_constraints": [
    {
      "name":        "node",
      "min_version":  "22.22.3",
      "use_system":   true
    }
  ],
  "payload": [
    {
      "src":  "payload/bin/%s",
      "dst":  "{{TARGET_ROOT}}/%s/%s/bin/%s",
      "mode": "0755"
    }
  ],
  "checksums": {
    "install.sh":              "sha256:TBD",
    "uninstall.sh":            "sha256:TBD",
    "payload/bin/%s":          "sha256:TBD"
  },
  "upgrade": {
    "strategy":         "replace",
    "compatible_from":  [],
    "migrations":       []
  },
  "scripts": {
    "install":   "install.sh",
    "uninstall": "uninstall.sh"
  },
  "services": [
    {
      "name":        "serve",
      "command":     [%q, "serve", "--port", "8080"],
      "restart":     "on-failure",
      "working_dir": "{{DEPLOY_ROOT}}",
      "description": "%s HTTP daemon, pinned to port 8080"
    }
  ]
}
`, name, source, version, name, name, version, name, name, name, name)
}

func renderInstallSh() string {
	return `#!/usr/bin/env bash
# install.sh — minimal installer; see docs/install-protocol.md
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAME=$(grep -o '"agent": *"[^"]*"' "$SCRIPT_DIR/manifest.json" | cut -d'"' -f4)
VERSION=$(grep -o '"version": *"[^"]*"' "$SCRIPT_DIR/manifest.json" | cut -d'"' -f4)
TARGET="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
INSTALL_DIR="$TARGET/$NAME/$VERSION"

mkdir -p "$INSTALL_DIR/payload" "$TARGET/bin"
cp -r "$SCRIPT_DIR/payload/." "$INSTALL_DIR/payload/"
ln -sf "$INSTALL_DIR/payload/bin/$NAME" "$TARGET/bin/$NAME"
"$TARGET/bin/$NAME" --version >/dev/null || { echo "agent didn't start" >&2; exit 1; }
echo "Installed $NAME $VERSION to $INSTALL_DIR"
`
}

func renderUninstallSh() string {
	return `#!/usr/bin/env bash
# uninstall.sh — inverse of install.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAME=$(grep -o '"agent": *"[^"]*"' "$SCRIPT_DIR/manifest.json" | cut -d'"' -f4)
VERSION=$(grep -o '"version": *"[^"]*"' "$SCRIPT_DIR/manifest.json" | cut -d'"' -f4)
TARGET="${AGENT_MARKETPLACE_TARGET_ROOT:-$HOME/.local}"
INSTALL_DIR="$TARGET/$NAME/$VERSION"

rm -rf "$INSTALL_DIR"
rm -f "$TARGET/bin/$NAME"
rm -f "$TARGET/state/$NAME.state.json"
echo "Uninstalled $NAME $VERSION"
`
}

func renderReadme(name string) string {
	return fmt.Sprintf(`# %s

First version of %s.

Generated by agentpkg package init.
`, name, name)
}

// agentTemplate export for docs (rare use)
var _ = agentTemplate
