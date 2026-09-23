# Skills HTTP API reference

Skills live in a parallel storage namespace alongside agents: `dist/skills/`
holds the zips + sha256 sidecars, `dist/skills-index.json` is the
self-describing snapshot. All routes are mounted under `/api/v1/skills/`
and require Basic Auth (single shared password).

The full OpenAPI 3.0 spec is at `/swagger/openapi.json` (live, served
by `marketplace-api`) and committed at `docs/api/openapi.json`.

## Endpoints

### Read

| Method | Path | |
|---|---|---|
| `GET` | `/api/v1/skills[?source=X&channel=Y]` | List skills (stripped index) |
| `GET` | `/api/v1/skills/{source}` | List skills under one source |
| `GET` | `/api/v1/skills/{source}/{name}` | One skill (all versions) |
| `GET` | `/api/v1/skills/{source}/{name}/{version}` | One skill version |
| `GET` | `/api/v1/skills/{source}/{name}/{version}/download` | Stream zip bytes |
| `GET` | `/api/v1/skills/{source}/{name}/{version}/sha256` | sha256 sidecar |
| `GET` | `/api/v1/skills/{source}/{name}/{version}/SKILL.md` | Raw SKILL.md (bytes) |
| `GET` | `/api/v1/skills-index.json` | Raw `dist/skills-index.json` (debug) |

### Write

| Method | Path | |
|---|---|---|
| `POST` | `/api/v1/skills` | Upload a skill zip (multipart) |
| `DELETE` | `/api/v1/skills/{source}/{name}` | Delete every version under one source |
| `DELETE` | `/api/v1/skills/{source}/{name}/{version}` | Delete one version |

All endpoints use the standard error envelope:

```json
{ "error": { "code": "not_found", "message": "..." } }
```

## Listing semantics

`GET /api/v1/skills` returns the **stripped** projection by default
(card-grid view): per-skill metadata + each version's `zip`
reference, but no per-version `body`, / inputs, / requires, or
 entry.

`?channel=` projects the response to **the latest version per skill
in that channel** (semver-max). Default is `stable`. `?channel=latest`
is an alias for `stable` (npm convention). Skills with no version in
the requested channel are omitted entirely.

`?source=` filters to a single source (community / internal). Without
it, the response is cross-source and skills from different sources
are listed separately by name.

## Upload contract

`POST /api/v1/skills` accepts `multipart/form-data` with a single
field:

| Field | Type | |
|---|---|---|
| `file` | binary | The skill zip. Filename MUST match `<name>-<source>-<version>.zip` |

Optional query parameter: `?channel=stable|beta|edge|internal`
(default: `stable`). The server-side channel is bound to the version
at upload time and is immutable.

Status codes:

| Code | Meaning |
|---|---|
| `201` | Created. Body is the new `SkillVersion` JSON. |
| `400` | SKILL.md missing, malformed, or `name`/`version` doesn't match the zip filename |
| `401` | Missing or invalid Basic Auth |
| `409` | `(name, source, version)` already exists. Bump the version to publish a fix. |
| `413` | Upload exceeds 50 MiB |
| `415` | Content-Type isn't `multipart/form-data` |
| `503` | Dist directory not configured (server misconfiguration) |

The server uses the zip **filename** as the authoritative identity for
`(name, source, version)` — not the SKILL.md frontmatter. A mismatch
between the filename and the manifest is a 400.

The whole upload critical section is serialized through an
in-process mutex (`State.SkillsDir.Lock`). Concurrent uploads against
the same registry won't tear `skills-index.json`.

## Delete contract

`DELETE /api/v1/skills/{source}/{name}/{version}` removes one specific
release. Returns `{"deleted": "<filename>"}` on success.

`DELETE /api/v1/skills/{source}/{name}` removes every version of that
skill under that source. Returns `{"deleted": ["file1.zip", ...]}`
with the list of removed zips.

Both modes rebuild `dist/skills-index.json` and atomically publish via
`State.SetSkillsIndex`. Missing- the is silent (idempotent delete).
The indexed entry not existing → 404.

## The `Index` schema (full)

`dist/skills-index.json` is the canonical snapshot of the registry.
Two response shapes share its keys:

- `Index` — full content with per-version `body`, / inputs,
  `requires`, `entry_point`.
- `IndexStripped` — list-view projection (no per-version
  body /
  inputs / / requires / `entry_point`).

Each `Skill` has skill-level fields (stable across versions) and a
list of `SkillVersion` entries. The full `SkillVersion` includes the
embedded SKILL.md body — UI list views don't need to unzip.

The `zip` field on each version points at the artifact on disk:

```json
{
  "filename": "my-skill-community-1.0.0.zip",
  "size_bytes": 12345,
  "sha256": "sha256:abcd..."
}
```

`sha256` uses the `sha256:<hex>` format for consistency with the
agent tarballs.

## Authentication

All routes under `/api/v1/skills/...` require Basic Auth:

```bash
curl -u "agentpkg:$PASSWORD" https://localhost:8443/api/v1/skills
```

(The username is always `agentpkg`; the password is the shared
operator-configured secret.) Use `agentpkg login --server ... --password-file ...`
to populate `~/.config/agentpkg/credentials`.