# Logo format

`meta.yaml`'s `logo` field carries the agent's logo as a string passed through verbatim to the marketplace-api consumer. There are four accepted shapes:

## 1. Empty string (default fallback)

```yaml
logo: ""
```

The consumer falls back to its own embedded default placeholder. Use this when the agent has no bespoke logo — it's the cheapest option.

## 2. `data:image/<mime>;base64,<payload>` — preferred for offline

```yaml
logo: "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciLz4="
```

Inline base64. **Use this for air-gapped / fully-offline deployments** — the bytes travel with `dist/index.json` and survive the upstream being unreachable after sync. The agentpkg validator only checks the MIME prefix (`image/`) and that the payload is non-empty; it does not decode the bytes.

Supported MIME types: `image/svg+xml`, `image/png`, `image/jpeg`, `image/webp`, `image/gif`. Consumers typically enforce a hard size cap (e.g. 2 MiB) at decode time.

## 3. `http://` or `https://` URL

```yaml
logo: "https://example.com/opencode.svg"
```

The consumer fetches the URL **once** at sync time, caches the bytes on local disk, and serves them via its own static handler. Use this when the upstream registry is reachable and you want to keep `meta.yaml` small. The URL must be a syntactically valid absolute URL with a host.

## 4. Bare filename — for pre-built templates with bundled assets

```yaml
logo: "opencode.png"
```

A bare filename (`<name>.<ext>`) references an image asset bundled in the consumer's frontend. The consumer rewrites it to a stable URL prefix (currently `/marketplace-logos/<file>` in the agent-platform-console) and the browser loads the asset directly from the console's static server — no backend fetch, no base64 in the index payload, no asset on disk in the backend's data dir.

**Use this only for templates whose images are part of the console's build output.** Today that's the three pre-built templates (`opencode`, `openclaw`, `hermes-agent`). User-defined templates added after a release **must** use option 2 (data URL) or option 3 (HTTP URL) — their images cannot land in the console bundle without a frontend rebuild.

The filename must:

- Match `[A-Za-z0-9._-]+\.(png|jpg|jpeg|gif|webp|svg)` (no path separators,
  no `..`, allow-list of extensions mirroring the consumer's `extForLogoMime`).
- Be the exact basename of a file that exists at `/marketplace-logos/<filename>`
  in the console's static assets. A typo'd filename renders the default
  placeholder (graceful degradation — never a row-rejecting error).

## Validation

`agentpkg package verify` (and `agentpkg package build`) check `meta.yaml` against the schema. On format failure the build aborts with a clear error message. Source of truth:

- `internal/manifest/meta.go` — `validateLogo` (format-only check) and
  `validateStaticAssetFilename` (bare-filename branch)
- `docs/meta-yaml-schema.md` — schema reference

Any other value (e.g. `robot`, `lucide:code`, an arbitrary string) is rejected.

## How it flows into the index

`agentpkg package build` reads meta.yaml's `logo`, validates the format, and the value is written verbatim into `dist/index.json` as `Agent.logo` (no rewriting, no decoding). The marketplace-api serves it as-is. The consumer (currently the agent-platform-backend) decides whether to fetch, decode, or rewrite to its bundled-asset URL.