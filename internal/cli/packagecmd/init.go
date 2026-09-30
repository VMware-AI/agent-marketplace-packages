// Package package implements the agentpkg package <subcommand> group.
//
// These commands are used by package authors — they scaffold, validate,
// build, sign, and reindex agent bundles. They all share the agents/<name>/
// on-disk layout.
package packagecmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
        ├── manifest.json         # technical manifest (schema_version, runtime_requirements, payload, ...)
        ├── install.sh            # ~70 line installer (system runtime + offline wheels)
        ├── uninstall.sh          # inverse of install.sh
        ├── payload/
        │   └── bin/<name>        # entry point (replace with your real binary)
        ├── migrate/              # cross-version migration scripts
        │   └── from-X.Y.Z.sh
        └── README.md             # version-specific notes
`

// metaTemplate is the boilerplate for meta.yaml. Field placeholders use
// Go template syntax (deliberately — see scaffoldAgent) so the values
// supplied by renderMeta land in the rendered file. Previous versions
// emitted literal `<display_name>` / `<category>` / etc. markers and
// left it to the operator to find-and-replace; package build then
// failed on those placeholders (validation rejects "utility"-less
// categories and empty descriptions) and on the matching sha256:TBD
// entries in manifest.json. Now we substitute real defaults at scaffold
// time so `package init && package build` succeeds out of the box.
const metaTemplate = `# agents/{{.Name}}/meta.yaml
#
# Agent-level marketing metadata. This file is version-independent:
# all versions of {{.Name}} share the same meta.yaml. agentpkg package build
# embeds a copy into every tarball (as the top-level meta.yaml file) and
# also flattens the merged form into dist/index.json so the marketplace
# frontend can render cards without parsing tarballs.

# Required: human-readable name shown in the UI.
# Falls back to the agent name if left blank.
display_name: "{{.DisplayName}}"

# Required: one-line description (10–200 chars). Shown on the card.
description: "{{.Description}}"

# Optional: logo. Leave empty to use the consumer's default placeholder.
# Accepts an http(s) URL or a data:image/<mime>;base64,... URL (preferred
# for offline / air-gapped deployments). See docs/logo-format.md.
logo: "{{.Logo}}"

# Required: one of the category IDs from docs/category-catalog.md.
category: "{{.Category}}"

# Optional: target deployment environment for this agent.
#   "vm"        — systemd --user on a VM / bare-metal host (default if unset)
#   "container" — single-container run
#   "k8s"       — helm chart or k8s-manifest driven install
# Stable across all versions of the agent; declared once in meta.yaml,
# not per-version in manifest.json. Defaults to "vm" if omitted.
runtime_type: "{{.RuntimeType}}"

# Optional: up to 10 search tags, lowercase, dash-separated.
tags:
{{- range .Tags}}
  - {{.}}
{{- end}}
`

// metaScaffoldData is the data passed to metaTemplate. Using named
// fields (not a map) keeps the {{...}} placeholders type-checked at
// template-execution time — a typo like {{.DispllayName}} becomes an
// "executing template: can't evaluate field DispllayName" error during
// init instead of a silent literal in the rendered file.
type metaScaffoldData struct {
	Name        string
	DisplayName string
	Description string
	Logo        string
	Category    string
	RuntimeType string
	Tags        []string
}

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
//
// F024 (tested 2026-09-30): the previous scaffoldAgent emitted literal
// `<display_name>` / `<category>` / `<runtime_type>` / `<logo>` /
// `<description>` markers in meta.yaml, `sha256:TBD` in all three
// manifest.checksums entries, and a `.gitkeep` file at the payload
// binary path. Operators had to hand-edit ~30 lines before `package
// build` would succeed — a high entry barrier for first-time authors.
// We now substitute sensible defaults in meta.yaml, compute the real
// sha256 of every payload file at scaffold time, and drop a working
// (if minimal) entry-point script that handles the install.sh smoke
// check (`$TARGET/bin/$NAME --version` → exit 0).
func scaffoldAgent(name, root string) error {
	if _, err := os.Stat(root); err == nil {
		return fmt.Errorf("directory %s already exists; refusing to overwrite", root)
	}

	// 1. Generate the file contents in memory FIRST so we can compute
	//    sha256s of the exact bytes that will land on disk. Computing
	//    sha256 from a tmpfile and then writing would work, but it
	//    would leave a stale tmpfile behind if the rename failed; the
	//    in-memory path is atomic-by-construction.
	installBody := renderInstallSh(name)
	uninstallBody := renderUninstallSh(name)
	binaryBody := renderPayloadBinary(name)

	// 2. Compute real sha256s for manifest.checksums. Keep the
	//    manifest key paths identical to the previous template so
	//    downstream tooling that grep'd for "payload/bin/<name>" in
	//    the manifest keeps working.
	installSHA := "sha256:" + sha256Hex([]byte(installBody))
	uninstallSHA := "sha256:" + sha256Hex([]byte(uninstallBody))
	binarySHA := "sha256:" + sha256Hex([]byte(binaryBody))

	files := map[string]string{
		"meta.yaml":                           renderMeta(name),
		"upstream/0.1.0/manifest.json":        renderManifest(name, "upstream", "0.1.0", installSHA, uninstallSHA, binarySHA),
		"upstream/0.1.0/install.sh":           installBody,
		"upstream/0.1.0/uninstall.sh":         uninstallBody,
		"upstream/0.1.0/README.md":            renderReadme(name),
		"upstream/0.1.0/payload/bin/<name>":   binaryBody,
		"upstream/0.1.0/migrate/.gitkeep":     "# cross-version migration scripts\n",
	}

	for rel, body := range files {
		// Substitute `<name>` with the real agent name in the file
		// path so the scaffolded binary lives at the path manifest
		// references (which itself used the same substitution).
		if rel == "upstream/0.1.0/payload/bin/<name>" {
			rel = "upstream/0.1.0/payload/bin/" + name
		}
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(full), err)
		}
		// install.sh / uninstall.sh / the placeholder binary must
		// be executable — package verify and the consumer's install
		// pipeline rely on the +x bit. The previous scaffoldAgent
		// emitted these at the default 0644, which silently passed
		// the build (no exec at build time) but broke the first
		// install run with EACCES. Write mode 0755 here so a clean
		// `package init && package build && install` chain works.
		mode := os.FileMode(0644)
		if filepath.Base(rel) == "install.sh" ||
			filepath.Base(rel) == "uninstall.sh" ||
			filepath.Base(rel) == name ||
			(rel != "meta.yaml" && filepath.Base(rel) != "README.md" &&
				filepath.Base(rel) != ".gitkeep") {
			mode = 0755
		}
		if err := os.WriteFile(full, []byte(body), mode); err != nil {
			return fmt.Errorf("write %s: %w", full, err)
		}
		fmt.Println("  +", full)
	}
	fmt.Println()
	fmt.Printf("Scaffolded %s\n", root)
	fmt.Println("Next steps:")
	fmt.Println("  1. (optional) edit agents/" + name + "/meta.yaml — refine description / add logo / adjust tags")
	fmt.Println("  2. (optional) edit agents/" + name + "/upstream/0.1.0/manifest.json — set runtime_requirements, requires")
	fmt.Println("  3. replace agents/" + name + "/upstream/0.1.0/payload/bin/" + name + " with your real entry-point binary")
	fmt.Println("  4. agentpkg package verify agents/" + name)
	fmt.Println("  5. agentpkg package build agents/" + name + " --version 0.1.0")
	return nil
}

// renderMeta produces a starter meta.yaml for the given agent name.
// All values are real (not `<...>` placeholders) and pass the meta
// schema (LoadMeta + Validate), so `package verify` accepts the
// scaffolded directory without any hand-editing.
func renderMeta(name string) string {
	tmpl, err := template.New("meta").Parse(metaTemplate)
	if err != nil {
		// parse errors are static — a bug in metaTemplate surfaces
		// here during the binary's startup, not at scaffold time.
		// Falling back to the literal template would still emit
		// the placeholders we're trying to eliminate; better to
		// panic so the bug is caught at first run.
		panic(fmt.Sprintf("parse metaTemplate: %v", err))
	}
	// Description must be 10–200 chars (manifest.meta.go Validate).
	// We deliberately use a string that's >10 chars even for the
	// shortest plausible agent name; "An agent named X." (16+ chars)
	// passes, and "agent" (5 chars) doesn't.
	data := metaScaffoldData{
		Name:        name,
		DisplayName: name,
		Description: fmt.Sprintf("An agent named %s — scaffolded by `agentpkg package init`.", name),
		Logo:        "",
		Category:    "utility", // in validCategories (manifest.catalogs.go)
		RuntimeType: "vm",      // valid default per meta.RuntimeType
		Tags:        []string{},
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		// Same rationale as the Parse error above: we don't want
		// to silently fall back to the literal template.
		panic(fmt.Sprintf("execute metaTemplate: %v", err))
	}
	return buf.String()
}

// renderManifest produces manifest.json for the given agent. The
// checksums for install.sh / uninstall.sh / payload/bin/<name> are
// passed in (precomputed from the byte-for-byte install body, etc.)
// so the scaffolded manifest matches the on-disk files exactly —
// `package verify` walks manifest.checksums and re-hashes each
// payload file, and any mismatch is a hard error.
func renderManifest(name, source, version, installSHA, uninstallSHA, binarySHA string) string {
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
  "runtime_requirements": [
    {
      "name":        "node",
      "version":     ">=22.22.3",
      "verify_cmd":  "node --version",
      "install_hint": "apt install -y nodejs (Ubuntu 22.04+) or use nvm: 'nvm install 22'"
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
    "install.sh":              %q,
    "uninstall.sh":            %q,
    "payload/bin/%s":          %q
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
`, name, source, version, name, name, version, name, installSHA, uninstallSHA, name, binarySHA, name, name)
}

// renderInstallSh produces a minimal but real installer script. The
// previous version emitted `install.sh` with the same body — it's
// unchanged; what changed is that we now compute its sha256 and
// embed it into manifest.checksums. Pass the agent name so future
// iterations could template the script (currently it greps name out
// of manifest.json, same as before).
func renderInstallSh(name string) string {
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

// renderUninstallSh produces the inverse of install.sh. Unchanged from
// the previous version; sha256 is now embedded into manifest.checksums.
func renderUninstallSh(name string) string {
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

// renderPayloadBinary produces a placeholder entry-point script that
// handles the install.sh smoke check (`bin/<name> --version` → exit 0).
// Operators replace it with their real binary. The script is intentionally
// minimal — no flag parsing, no config loading, just enough for the
// scaffolded `package init && package build && install` chain to round-trip.
//
// Two responsibilities:
//   - `--version` → echo "<name> <version>" + exit 0 (install.sh checks this)
//   - no args or any other flag → friendly message + exit 0 (so `agentpkg install`
//     doesn't fail post-install when systemd tries ExecStart=<name> serve ...)
func renderPayloadBinary(name string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
# %[1]s entry-point scaffold.
#
# Generated by `+"`agentpkg package init`"+`. Replace this file with your
# real binary before publishing. The scaffolded installer
# (upstream/0.1.0/install.sh) invokes `+"`$TARGET/bin/%[1]s --version`"+`
# to confirm the binary is executable; this script returns 0 so the
# round-trip `+"`package init && package build && agentpkg install`"+`
# succeeds end-to-end.

case "$1" in
  --version)
    echo "%[1]s 0.1.0 (scaffold placeholder)"
    exit 0
    ;;
  *)
    echo "%[1]s scaffold placeholder — replace payload/bin/%[1]s with your real binary."
    exit 0
    ;;
esac
`, name)
}

func renderReadme(name string) string {
	return fmt.Sprintf(`# %s

First version of %s.

Generated by agentpkg package init.
`, name, name)
}

// sha256Hex returns the lowercase hex sha256 of b. Used by scaffoldAgent
// to populate manifest.checksums with values that match the actual file
// bytes (which `package verify` will recompute and compare against).
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// agentTemplate export for docs (rare use)
var _ = agentTemplate
