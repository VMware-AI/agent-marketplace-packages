# Contributing — adding a new (agent, version) bundle

This SOP walks through packaging a new version of an existing agent. For a brand-new agent, see [Adding a new agent](#adding-a-new-agent) at the bottom.

## Prerequisites

You need on the build host:
- `bash ≥ 4`, `coreutils`, `findutils`, `grep`, `sed`, `awk`, `tar`, `gzip`, `sha256sum`, `curl`
- `node` (only for `npm pack` when packaging openclaw)
- `python3 ≥ 3.11` + `pip` (only when packaging hermes-agent)
- Network access to the upstream registry (npm / PyPI / GitHub Releases)

## Step 1 — pick the source subtree

Pick `upstream/` if you're mirroring a published upstream release as-is. Pick `ours/` only if this is an internal build (fork, patch, re-distribution).

```bash
mkdir -p agents/<agent>/upstream/<version>
cd agents/<agent>/upstream/<version>
mkdir -p payload runtime files migrate
```

The directory you just created **is** the root of the future tarball — everything goes here.

## Step 2 — fetch the upstream artifact

Use the right helper for the agent:

```bash
# opencode — static binary from GitHub Releases
tools/fetch.sh opencode upstream 0.0.55

# openclaw — npm tarball
tools/fetch.sh openclaw upstream 2026.7.1-2

# hermes-agent — pip wheels (all transitive deps, prefers prebuilt wheels)
tools/fetch.sh hermes-agent upstream 0.18.2
```

Each helper:
1. Resolves the canonical upstream URL (and pins a SHA256 of the upstream artifact).
2. Downloads it into `payload/`.
3. Writes a stub `manifest.json` with `agent`, `source`, `version`, and `upstream.sha256`.

Re-run is safe — it compares local SHA256 to upstream SHA256 and refuses to overwrite a diverged payload.

## Step 3 — pre-resolve install deps for offline installs

The agent tarball must install with **no network on the target**. After fetch.sh populates `payload/`, pre-resolve the full dependency tree:

```bash
# openclaw: resolve npm tree into payload/openclaw/ (full --global prefix layout)
NPM_REGISTRY=https://registry.npmmirror.com/ \
  npm install --prefix agents/openclaw/upstream/<v>/payload/openclaw \
    --global --no-audit --no-fund \
    openclaw@<v>

# hermes-agent: download all transitive wheels (linux manylinux) into payload/wheels/
python3 -m pip download \
  --dest agents/hermes-agent/upstream/<v>/payload/wheels \
  --index-url https://mirrors.aliyun.com/pypi/simple/ \
  --python-version 3.12 \
  --platform manylinux2014_x86_64 \
  --only-binary=:all: \
  "hermes-agent==<v>"
```

`tools/pack.sh` then packs whatever's under `payload/`. At install time the vendored tree is consumed offline (`cp -a payload/openclaw/. $DEPLOY_ROOT/` for npm, `uv pip install --no-index --find-links payload/wheels/` for Python).

Runtimes (Node, Python, uv) are **not** in the tarball — they're installed on the target machine separately via `tools/install-runtime.sh` (Ubuntu 24.04 only).

## Step 4 — write `manifest.json`

Use the schema in [`docs/manifest-schema.md`](docs/manifest-schema.md). At minimum, fill in:

- `agent`, `source`, `version`, `channel`
- `upstream.sha256` (filled by the fetch step)
- `runtime_requirements[]` — list of system-installed runtimes the agent needs
  (e.g. `{"name":"node","version":">=22.22.3 <23, >=24.15.0 <25, or >=25.9.0"}`)
- `payload[]` — files to deploy into `$HOME/.local/<agent>/…`
- `requires.system_packages`, `requires.system_tools`
- `upgrade.compatible_from`, `upgrade.migrations` (see [`docs/upgrade-protocol.md`](docs/upgrade-protocol.md))

You don't have to fill `checksums` by hand — `tools/pack.sh` writes them for you.

## Step 4b — pre-resolve install deps for offline installs

The agent tarball must install with no network on the target. After fetch.sh populates `payload/`, pre-resolve the full dependency tree:

```bash
# openclaw: resolve npm tree into payload/openclaw/ (full prefix layout)
NPM_REGISTRY=https://registry.npmmirror.com/ \
  npm install --prefix agents/openclaw/upstream/<v>/payload/openclaw \
    --global --no-audit --no-fund \
    openclaw@<v>

# hermes-agent: download all transitive wheels (linux manylinux) into payload/wheels/
python3 -m pip download \
  --dest agents/hermes-agent/upstream/<v>/payload/wheels \
  --index-url https://mirrors.aliyun.com/pypi/simple/ \
  --python-version 3.12 \
  --platform manylinux2014_x86_64 \
  --only-binary=:all: \
  "hermes-agent==<v>"
```

`tools/pack.sh` will pack whatever's under `payload/`. install.sh uses the vendored tree offline (`cp -a payload/openclaw/. $DEPLOY_ROOT/` for npm, `uv pip install --no-index --find-links payload/wheels/` for Python).

## Step 5 — write `install.sh`

All install scripts follow the contract in [`docs/install-protocol.md`](docs/install-protocol.md). Minimum required behavior:

1. Re-verify this tarball's SHA256 against `manifest.tarball.sha256`. Fail loudly if it doesn't match.
2. Re-verify each file's SHA256 against `manifest.checksums`. Refuse to install on mismatch.
3. Check `requires.system_tools` are on `PATH`; refuse with exit 40 if not.
4. **Verify runtime requirements** from `manifest.runtime_requirements` against the target machine. Fail with exit 50 (and print the `install_hint`) if the runtime is missing or the wrong version.
5. Read `$HOME/.local/state/<agent>.state.json` (if it exists) and decide: fresh install vs upgrade.
6. Deploy runtime + payload into `$HOME/.local/<agent>/…` (using only user-writable paths).
7. Write the new `state.json`.
8. Run `<agent> --version` to verify. Print `INSTALLED_VERSION=<output>` on success.
9. Exit 0 on success.

Runtimes (Node, Python, uv) are **NOT** bundled in the tarball — they're expected on the target machine. See Step 3 of the consumer Quick start for how to install them via `tools/install-runtime.sh`.

See the existing version directories (`agents/opencode/upstream/0.0.55/install.sh`, etc.) for the canonical templates.

## Step 6 — write `uninstall.sh`

The companion script. Must be the exact reverse: read `state.json`, remove every file listed under `installed_files`, then delete `state.json` itself. Exit 0 if the state is already gone.

## Step 7 — write `migrate/`

If this is a **major** version bump (per the version scheme used by the agent), add at least one migration entry under `migrate/`:

- `migrate/from-<previous-major-minor>.x.sh` — runs when upgrading from any prior minor in the same major
- `migrate/from-<exact>.sh` — runs only for that exact prior version

`install.sh` matches scripts in this priority order (see `docs/upgrade-protocol.md`):

1. Exact match: `from-<installed-version>.sh`
2. Major-minor wildcard: `from-<major>.<minor>.x.sh`
3. Fallback: `_link-upgrade.sh` (auto-downloads intermediate versions if available)

## Step 8 — verify the directory

```bash
tools/verify.sh <agent> <source> <version>
```

This will:
1. Validate `manifest.json` against the schema
2. Re-hash every file under `payload/`, `runtime/`, `files/` and compare to `manifest.checksums`
3. Run `install.sh --self-test` if you've implemented that flag (optional but recommended)

It refuses to pass until everything is consistent.

## Step 9 — pack

```bash
tools/pack.sh <agent> <source> <version>
```

Produces:
- `dist/<agent>-<source>-<version>.tar.gz`
- `dist/<agent>-<source>-<version>.tar.gz.sha256`

The tarball **is** the contents of `agents/<agent>/<source>/<version>/` (excluding `state*.json` test artifacts).

## Step 10 — sign (optional, recommended for production)

```bash
tools/sign.sh <agent> <source> <version>
```

Produces `dist/<agent>-<source>-<version>.tar.gz.sig` (GPG detached signature).

## Step 11 — publish (optional)

```bash
tools/publish.sh <agent> <source> <version> --target s3://your-bucket
```

This is the only step you need to implement yourself — the script is a stub. Adjust it to whatever CDN/registry you use.

---

## Adding a new agent

For a brand-new agent, the work is:
1. Create `agents/<new-agent>/upstream/1.0.0/` (or appropriate starting version)
2. Add a `_templates/<new-agent>-manifest.json.tmpl` for future versions
3. Document any agent-specific quirks in `agents/<new-agent>/README.md`
4. Update `README.md`'s "Currently packaged" table
5. Declare the runtime requirements in the agent's `manifest.json` (under `runtime_requirements[]`); consumers run `tools/install-runtime.sh install --from-manifest <path>` on their Ubuntu 24.04 target to provision them.

---

## Versioning conventions

| Agent        | Scheme                | Example              |
|--------------|-----------------------|----------------------|
| opencode     | `MAJOR.MINOR.PATCH`   | `0.0.55`             |
| openclaw     | CalVer (`YYYY.M.P`)   | `2026.7.2`           |
| hermes-agent | `MAJOR.MINOR.PATCH`   | `0.18.2`             |

Do not invent your own scheme — use whatever the upstream project uses, so `compatible_from` matching keeps working.