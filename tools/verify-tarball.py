#!/usr/bin/env python3
"""verify-tarball.py — check that a built tarball still matches its source bundle.

Usage:
    tools/verify-tarball.py <bundle-dir> <tarball>

`<bundle-dir>` is `agents/<agent>/<source>/<version>` — the directory that was
packed.  `<tarball>` is the `.tar.gz` produced from it.

Why this exists
---------------
A tarball is not a faithful copy of its source by default.  `tar -h` /
`--dereference` (and any tool that follows symlinks while writing) silently
replaces a symlink with a *copy of its target*.  That is invisible in a file
listing — the path is still there, only the type changed — and it breaks the
openclaw launcher:

    payload/openclaw/bin/openclaw -> ../lib/node_modules/openclaw/openclaw.mjs

The launcher resolves `./dist/entry.js` against its own `import.meta.url`.
While `bin/openclaw` is a symlink, Node resolves that URL to the *real* path
`lib/node_modules/openclaw/openclaw.mjs`, so the lookup lands next to `dist/`.
Once the link is dereferenced into a copy, the URL points at `bin/`, the
lookup misses, and the gateway dies with:

    Error: openclaw: missing dist/entry.(m)js (build output).

Neither the installer's `openclaw --version` nor the manifest checksum check
catches this: `--version` takes the launcher's fast path and never imports
`dist/entry.js`, and the checksum map only covers the handful of top-level
files listed in `manifest.payload`, not the whole payload tree.

What it checks
--------------
1. Every path in the source bundle is present in the tarball.
2. Every symlink in the source bundle is stored in the tarball *as a symlink*
   with the same target — not dereferenced into a copy.
3. The tarball contains no paths that are not in the source bundle (apart from
   the agent-level `meta.yaml`, which pack.sh embeds at the tarball root).

Exit codes:
    0  tarball matches the source bundle
    1  mismatch (missing path, dereferenced symlink, unexpected path)
    2  usage / bundle-layout error
"""

from __future__ import annotations

import os
import sys
import tarfile


def source_entries(bundle_dir):
    """Map every non-directory path in the bundle to its kind and link target.

    Returns {relative_path: ("file", None)} or {relative_path: ("symlink", target)}.
    """
    entries = {}
    for dirpath, dirnames, filenames in os.walk(bundle_dir):
        # Do not descend into symlinked directories — they are entries in their
        # own right, not containers.
        dirnames[:] = [d for d in dirnames if not os.path.islink(os.path.join(dirpath, d))]
        for name in dirnames + filenames:
            full = os.path.join(dirpath, name)
            rel = os.path.relpath(full, bundle_dir)
            if os.path.islink(full):
                entries[rel] = ("symlink", os.readlink(full))
            elif os.path.isfile(full):
                entries[rel] = ("file", None)
    return entries


def tarball_entries(tarball, prefix):
    """Map every non-directory member of the tarball to its kind and link target.

    `prefix` is the tarball's top-level directory (the version dir name).
    Members outside it are returned under their own name so the caller can
    report them.
    """
    entries = {}
    with tarfile.open(tarball, "r:gz") as archive:
        for member in archive.getmembers():
            if member.isdir():
                continue
            name = member.name
            if name.startswith(prefix + "/"):
                name = name[len(prefix) + 1 :]
            if member.issym():
                entries[name] = ("symlink", member.linkname)
            else:
                entries[name] = ("file", None)
    return entries


def expected_root_files(bundle_dir):
    """Root-level files the tarball is allowed to carry beyond the bundle dir.

    pack.sh embeds the agent-level `meta.yaml` (two levels above the bundle
    dir) at the tarball root so the marketplace index can populate each
    agent's display fields.
    """
    agent_root = os.path.dirname(os.path.dirname(os.path.abspath(bundle_dir)))
    meta = os.path.join(agent_root, "meta.yaml")
    return {"meta.yaml"} if os.path.isfile(meta) else set()


def main(argv):
    if len(argv) != 3:
        sys.stderr.write(__doc__)
        return 2

    bundle_dir = os.path.normpath(argv[1])
    tarball = argv[2]
    if not os.path.isdir(bundle_dir):
        sys.stderr.write("✗ %s: not a directory\n" % bundle_dir)
        return 2
    if not os.path.isfile(tarball):
        sys.stderr.write("✗ %s: not a file\n" % tarball)
        return 2

    prefix = os.path.basename(bundle_dir)
    source = source_entries(bundle_dir)
    packed = tarball_entries(tarball, prefix)
    allowed_root = expected_root_files(bundle_dir)

    problems = 0

    missing = sorted(set(source) - set(packed))
    for rel in missing:
        sys.stderr.write("✗ %s: in source but not in tarball\n" % rel)
        problems += 1

    dereferenced = []
    for rel, (kind, target) in sorted(source.items()):
        if kind != "symlink":
            continue
        got = packed.get(rel)
        if got is None:
            continue  # already reported as missing
        if got[0] != "symlink":
            dereferenced.append((rel, target, got[0]))
        elif got[1] != target:
            sys.stderr.write(
                "✗ %s: symlink target changed: source %r, tarball %r\n"
                % (rel, target, got[1])
            )
            problems += 1

    for rel, target, got_kind in dereferenced:
        sys.stderr.write(
            "✗ %s: symlink was dereferenced into a %s during packaging "
            "(source target %r)\n" % (rel, got_kind, target)
        )
        problems += 1

    extra = sorted(set(packed) - set(source))
    for rel in extra:
        if rel in allowed_root:
            continue
        sys.stderr.write("✗ %s: in tarball but not in source\n" % rel)
        problems += 1

    if problems:
        sys.stderr.write(
            "\n✗ %s: %d problem(s) — the tarball is not a faithful copy of %s\n"
            % (tarball, problems, bundle_dir)
        )
        if dereferenced:
            sys.stderr.write(
                "  A dereferenced symlink changes the tree's shape at install\n"
                "  time.  For the openclaw launcher that is fatal: it resolves\n"
                "  './dist/entry.js' against its own import.meta.url, so once\n"
                "  bin/openclaw is a copy instead of a link the lookup misses\n"
                "  and the gateway dies with 'missing dist/entry.(m)js'.\n"
            )
        return 1

    sys.stderr.write(
        "✓ %s: matches %s (%d paths, symlinks preserved)\n"
        % (tarball, bundle_dir, len(source))
    )
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
