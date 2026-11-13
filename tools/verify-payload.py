#!/usr/bin/env python3
"""verify-payload.py — check that every relative import inside an agent payload resolves.

Usage:
    tools/verify-payload.py <bundle-dir> [<bundle-dir> ...]

A bundle dir is `agents/<agent>/<source>/<version>` — the directory that holds
`manifest.json` and `payload/`.

Why this exists
---------------
Agent payloads are vendored upstream trees (npm trees, wheels, binaries) that
are materialised locally and shipped inside the marketplace tarball.  Nothing
in the build verified that the vendored tree was *complete*.  When a chunk was
missing, the install still succeeded — `openclaw --version` passes because the
missing module is only reached through a lazy `await import(...)` — and the
crash surfaced days later as:

    Gateway failed to start: Cannot find module
    '.../dist/server-node-session-runtime-jbxtNSFm.js' imported from
    '.../dist/server.impl-qYPVZMND.js'

This script is the guard that turns that class of silent breakage into a build
failure.  It walks every JavaScript file in the payload, collects the relative
specifiers it imports, and asserts each one resolves to a file that exists.

Scope
-----
Only *module* specifiers are checked — relative specifiers that name a
JavaScript file (`.js` / `.mjs` / `.cjs`).  Extension-less CommonJS requires
(`require('./utils')`) are deliberately ignored: those live in dependency
bundles whose internal layout does not match the source layout the bundle was
built from, so they are not evidence of a missing file.  Dependencies are
integrity-checked by npm against `npm-shrinkwrap.json`; the *vendored*
package's own `dist/` is the part that gets copied around by hand, and that
is where the missing chunks were.

Type-only files (`*.d.ts`) are skipped — `import type ... from "./x"` is
erased at build time and the target need not exist at runtime.

Exit codes:
    0  every relative module import resolves
    1  at least one relative import does not resolve
    2  usage / bundle-layout error
"""

from __future__ import annotations

import os
import re
import sys

JS_SUFFIXES = (".js", ".mjs", ".cjs")

# A relative specifier: ./foo or ../foo/bar.  Deliberately conservative —
# it must not contain an interpolation, a backtick or a newline, otherwise it
# is a template literal we cannot resolve statically.
SPECIFIER_RE = re.compile(r"""['"](\.{1,2}/[^'"\n`\\]*)['"]""")

# A specifier that names a JavaScript module.  Everything else ("./x.ts",
# "./docs/foo.md", "./some/dir") is not a runtime module reference.
MODULE_SPECIFIER_RE = re.compile(r"\.m?js$")

# Contexts in which a quoted string is an import specifier.  Note that
# `new URL(..., import.meta.url)` is deliberately absent: it builds a path,
# it does not load a module.
IMPORT_CONTEXT_RE = re.compile(
    r"""(?:\bfrom\b\s*|\bimport\s*\(|\brequire\s*\()\s*$"""
)

# Directories that are not part of the runtime module graph of the vendored
# package:
#   node_modules — dependency trees, integrity-verified by npm against
#                  npm-shrinkwrap.json at install time;
#   scripts      — build/dev tooling that ships incomplete by design (the
#                  upstream `files` allowlist omits its helper modules).
# (See js_files() for how the two are told apart — the vendored package
# itself lives inside a node_modules directory.)


def strip_comments(source: str) -> str:
    """Return `source` with block and line comments removed, string literals kept.

    Without this, a doc comment that mentions a source path (`import x from
    "./src/entry.ts"`) is indistinguishable from a real import.
    """
    out = []
    index = 0
    length = len(source)
    while index < length:
        char = source[index]
        if char in "\"'`":
            quote = char
            cursor = index + 1
            while cursor < length:
                if source[cursor] == "\\":
                    cursor += 2
                    continue
                if source[cursor] == quote:
                    cursor += 1
                    break
                cursor += 1
            out.append(source[index:cursor])
            index = cursor
            continue
        if char == "/" and index + 1 < length and source[index + 1] == "/":
            newline = source.find("\n", index)
            index = length if newline == -1 else newline
            continue
        if char == "/" and index + 1 < length and source[index + 1] == "*":
            end = source.find("*/", index + 2)
            index = length if end == -1 else end + 2
            continue
        out.append(char)
        index += 1
    return "".join(out)


def looks_like_import_context(source: str, quote_start: int) -> bool:
    """True when the string literal starting at `quote_start` is an import specifier.

    `quote_start` is the index of the opening quote, so the context window
    must stop *before* it — otherwise `import("./x.js")` would look like
    `import("` and no context would match.

    The keyword has to sit on the same line as the specifier and that line
    must not look like a comment.  Bundled code routinely carries JSDoc
    blocks that spell out the module's public API, e.g.

        *   Options
        * } from './types.js'
        */

    which is a doc comment, not an import.
    """
    prefix = source[max(0, quote_start - 400) : quote_start]
    line_start = prefix.rfind("\n") + 1
    same_line = prefix[line_start:]
    stripped = same_line.lstrip()
    if stripped.startswith("*") or stripped.startswith("//"):
        return False
    if "*/" in same_line or "/*" in same_line:
        return False
    return bool(IMPORT_CONTEXT_RE.search(same_line))


def iter_specifiers(source: str):
    """Yield relative import specifiers found in a JavaScript source string."""
    for match in SPECIFIER_RE.finditer(source):
        specifier = match.group(1)
        if "${" in specifier:
            continue
        if not looks_like_import_context(source, match.start()):
            continue
        yield specifier


def is_module_specifier(specifier: str) -> bool:
    """True when the specifier names a JavaScript module (not a doc path or a dir)."""
    clean = specifier.split("?", 1)[0].split("#", 1)[0]
    if clean.endswith("/") or clean.endswith("."):
        return False
    base = os.path.basename(clean)
    if not base or base.startswith("."):
        return False
    return bool(MODULE_SPECIFIER_RE.search(base))


def resolves(importer_dir: str, specifier: str) -> bool:
    """True when `specifier` (relative to `importer_dir`) names an existing file."""
    clean = specifier.split("?", 1)[0].split("#", 1)[0]
    target = os.path.normpath(os.path.join(importer_dir, clean))
    return os.path.isfile(target)


def js_files(root: str):
    """Yield every JS file in `root` that belongs to the vendored package.

    Dependency trees are skipped — but only the *nested* ones.  An npm
    payload is laid out as `payload/<prefix>/lib/node_modules/<pkg>/`, so the
    vendored package itself sits inside a `node_modules` directory; skipping
    every `node_modules` would skip the very thing we want to check.
    """
    for dirpath, dirnames, filenames in os.walk(root):
        rel = os.path.relpath(dirpath, root)
        inside_node_modules = "node_modules" in rel.split(os.sep)
        kept = []
        for name in sorted(dirnames):
            if name == "scripts":
                continue
            if name == "node_modules" and inside_node_modules:
                continue
            kept.append(name)
        dirnames[:] = kept

        for filename in sorted(filenames):
            if filename.endswith(".d.ts"):
                continue
            if filename.endswith(JS_SUFFIXES):
                yield os.path.join(dirpath, filename)


def check_payload(payload_dir: str):
    """Return a list of (importer, specifier) pairs whose target is missing."""
    broken = []
    for path in js_files(payload_dir):
        try:
            with open(path, "r", encoding="utf-8", errors="replace") as handle:
                source = handle.read()
        except OSError as exc:
            broken.append((path, "<unreadable: %s>" % exc))
            continue
        source = strip_comments(source)
        importer_dir = os.path.dirname(path)
        for specifier in iter_specifiers(source):
            if not is_module_specifier(specifier):
                continue
            if not resolves(importer_dir, specifier):
                broken.append((path, specifier))
    return broken


def main(argv):
    if len(argv) < 2:
        sys.stderr.write(__doc__)
        return 2

    total_broken = 0
    for bundle_dir in argv[1:]:
        payload_dir = os.path.join(bundle_dir, "payload")
        if not os.path.isdir(payload_dir):
            sys.stderr.write("✗ %s: no payload/ directory\n" % bundle_dir)
            return 2

        broken = check_payload(payload_dir)
        if broken:
            total_broken += len(broken)
            for importer, specifier in broken:
                sys.stderr.write("✗ %s: unresolved import %r\n" % (importer, specifier))
            sys.stderr.write(
                "✗ %s: %d unresolved relative import(s) in payload\n"
                % (bundle_dir, len(broken))
            )
        else:
            sys.stderr.write("✓ %s: payload imports resolve\n" % bundle_dir)

    if total_broken:
        sys.stderr.write(
            "\npayload is incomplete — the vendored upstream tree is missing files.\n"
            "Re-materialise the payload (see CONTRIBUTING.md) before packing.\n"
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
