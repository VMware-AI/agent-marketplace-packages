#!/usr/bin/env python3
"""Build docs/api/openapi.json from the agent OpenAPI spec + skills additions.

Reads the git HEAD version via git show, applies structured edits, writes
the result. Run with: python3 tools/build_openapi.py

After running, also run `make openapi-embed` to keep the internal copy
in sync.
"""
import json
import subprocess
import sys
from pathlib import Path

ROOT = Path("/Users/gary/code/agent-marketplace-packages")
DOCS = ROOT / "docs/api/openapi.json"


def main():
    result = subprocess.run(
        ["git", "show", "HEAD:docs/api/openapi.json"],
        cwd=ROOT, capture_output=True, text=True, check=True,
    )
    spec = json.loads(result.stdout)

    # 1. Add runtime_type to Agent and AgentStripped (drift fix from HEAD).
    runtime_type_prop = {
        "type": "string",
        "enum": ["vm", "container", "k8s"],
        "description": "Deployment target declared in meta.yaml — stable across all versions of the agent.",
    }
    spec["components"]["schemas"]["Agent"]["properties"]["runtime_type"] = runtime_type_prop
    spec["components"]["schemas"]["AgentStripped"]["properties"]["runtime_type"] = runtime_type_prop

    # 2. Add skill schemas.
    skill_schemas = {
        "SkillIndex": {
            "type": "object",
            "description": "Full content of dist/skills-index.json. Each Skill's Versions include the embedded SKILL.md body so UI list views don't need to unzip.",
            "properties": {
                "generated_at": {"type": "string"},
                "schema_version": {"type": "string"},
                "skills": {"type": "array", "items": {"$ref": "#/components/schemas/Skill"}},
            },
        },
        "SkillIndexStripped": {
            "type": "object",
            "description": "Card-grid projection: drops per-version Body / Inputs / Requires / EntryPoint. Optional ?channel=X projects to the latest semver version per skill in that channel.",
            "properties": {
                "generated_at": {"type": "string"},
                "schema_version": {"type": "string"},
                "skills": {"type": "array", "items": {"$ref": "#/components/schemas/SkillStripped"}},
            },
        },
        "Skill": {
            "type": "object",
            "description": "One skill entry. Skill-level fields are stable across all versions.",
            "properties": {
                "name": {"type": "string", "description": "kebab-case, ^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$"},
                "display_name": {"type": "string"},
                "description": {"type": "string", "description": "1-1024 chars, no < or >"},
                "author": {"type": "string"},
                "tags": {"type": "array", "items": {"type": "string"}},
                "license": {"type": "string"},
                "homepage": {"type": "string", "description": "http(s) URL only"},
                "created_at": {"type": "string", "description": "YYYY-MM-DD"},
                "versions": {"type": "array", "items": {"$ref": "#/components/schemas/SkillVersion"}},
            },
        },
        "SkillStripped": {
            "type": "object",
            "properties": {
                "name": {"type": "string"},
                "display_name": {"type": "string"},
                "description": {"type": "string"},
                "author": {"type": "string"},
                "tags": {"type": "array", "items": {"type": "string"}},
                "license": {"type": "string"},
                "homepage": {"type": "string"},
                "created_at": {"type": "string"},
                "versions": {"type": "array", "items": {"$ref": "#/components/schemas/SkillVersionStripped"}},
            },
        },
        "SkillVersion": {
            "type": "object",
            "description": "One (source, channel, version) entry. Immutable once uploaded (409 on conflict).",
            "properties": {
                "version": {"type": "string", "description": "Strict semver MAJOR.MINOR.PATCH[-prerelease]"},
                "source": {"type": "string", "enum": ["community", "internal"]},
                "channel": {"type": "string", "enum": ["stable", "beta", "edge", "internal"]},
                "released_at": {"type": "string"},
                "requires": {"$ref": "#/components/schemas/Requires"},
                "inputs": {"type": "array", "items": {"$ref": "#/components/schemas/Input"}},
                "entry_point": {"type": "string", "description": "Path inside the zip"},
                "zip": {"$ref": "#/components/schemas/Zip"},
                "body": {"type": "string", "description": "Full SKILL.md text (frontmatter + body) embedded for UI convenience"},
            },
        },
        "SkillVersionStripped": {
            "type": "object",
            "properties": {
                "version": {"type": "string"},
                "source": {"type": "string"},
                "channel": {"type": "string"},
                "released_at": {"type": "string"},
                "zip": {"$ref": "#/components/schemas/Zip"},
            },
        },
        "SkillRequires": {
            "type": "object",
            "description": "Environment / tool requirements for running the skill.",
            "properties": {
                "os": {"type": "array", "items": {"type": "string", "enum": ["linux", "darwin", "windows"]}},
                "arch": {"type": "array", "items": {"type": "string", "enum": ["amd64", "arm64", "386", "arm"]}},
                "tools": {"type": "array", "items": {"type": "string", "description": "basename, e.g. 'bash', 'jq'"}},
            },
        },
        "SkillInput": {
            "type": "object",
            "description": "User-facing input field schema. Informational — runtime consumers decide whether to enforce.",
            "properties": {
                "name": {"type": "string"},
                "description": {"type": "string"},
                "required": {"type": "boolean"},
                "type": {"type": "string", "enum": ["string", "integer", "boolean", "number", "enum"]},
                "enum_values": {"type": "array", "items": {"type": "string"}, "description": "Required when type=enum"},
                "default": {"type": "string"},
            },
        },
        "SkillZip": {
            "type": "object",
            "properties": {
                "filename": {"type": "string", "description": "<name>-<source>-<version>.zip"},
                "size_bytes": {"type": "integer", "format": "int64"},
                "sha256": {"type": "string", "description": "sha256:<hex>"},
            },
        },
    }
    # Map openapi schema names to actual Go type names in internal/skills.
    # The Go types don't have a "Skill" prefix on some types (Input, Requires,
    # Zip, Index, IndexStripped) — only on the outer Skill/SkillVersion ones.
    schema_to_go_type = {
        "SkillIndex": "Index",
        "SkillIndexStripped": "IndexStripped",
        "SkillInput": "Input",
        "SkillRequires": "Requires",
        "SkillZip": "Zip",
    }
    for schema_name, go_type in schema_to_go_type.items():
        if schema_name in skill_schemas:
            skill_schemas[go_type] = skill_schemas.pop(schema_name)
    spec["components"]["schemas"].update(skill_schemas)

    # 3. Skill path parameters.
    spec["parameters"].update({
        "SkillSource": {"name": "source", "in": "path", "required": True, "schema": {"type": "string", "enum": ["community", "internal"]}},
        "SkillName": {"name": "name", "in": "path", "required": True, "schema": {"type": "string"}},
        "SkillVersion": {"name": "version", "in": "path", "required": True, "schema": {"type": "string"}},
    })

    # 4. New error responses.
    spec["responses"].update({
        "BadRequest": {"description": "Malformed request (missing multipart file, name mismatch between filename and SKILL.md, invalid channel, etc).", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}},
        "Conflict": {"description": "(name, source, version) already exists. Skill versions are immutable — bump the version to publish a fix.", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}},
        "PayloadTooLarge": {"description": "Upload exceeds the 50 MiB skill-zip cap.", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}},
        "UnsupportedMediaType": {"description": "POST must use multipart/form-data with a 'file' field.", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}},
    })

    # 5. Skill paths. Use Python literals to avoid brace-counting bugs.
    delete_all_responses_schema = {
        "type": "object",
        "properties": {"deleted": {"oneOf": [{"type": "string"}, {"type": "array", "items": {"type": "string"}}]}},
    }
    delete_single_responses_schema = {
        "type": "object",
        "properties": {"deleted": {"type": "string"}},
    }

    def err(ref):
        return {"$ref": f"#/components/responses/{ref}"}

    def ok_json(schema_ref):
        return {"description": "OK.", "content": {"application/json": {"schema": {"$ref": f"#/components/schemas/{schema_ref}"}}}}

    def ok_zip():
        return {"description": "OK.", "content": {"application/zip": {"schema": {"type": "string", "format": "binary"}}}}

    def ok_text(mime):
        return {"description": "OK.", "content": {mime: {"schema": {"type": "string"}}}}

    skill_paths = {
        "/api/v1/skills": {
            "get": {
                "tags": ["skills"],
                "summary": "List all skills (stripped index).",
                "description": "Optional ?source=community|internal filters to one source. Optional ?channel=stable|beta|edge|internal|latest projects each skill to its highest-semver version in that channel (latest is an alias for stable, npm-style). Default channel=stable.",
                "parameters": [
                    {"name": "source", "in": "query", "schema": {"type": "string", "enum": ["community", "internal"]}},
                    {"name": "channel", "in": "query", "schema": {"type": "string", "enum": ["stable", "beta", "edge", "internal", "latest"]}},
                ],
                "responses": {
                    "200": ok_json("IndexStripped"),
                    "401": err("Unauthorized"),
                    "503": err("ServiceUnavailable"),
                },
            },
            "post": {
                "tags": ["skills"],
                "summary": "Upload a skill zip (multipart/form-data, file field).",
                "description": "The zip filename must be <name>-<source>-<version>.zip; the server uses the filename as the authoritative identity (not the SKILL.md frontmatter). Re-uploading the same (name, source, version) returns 409. Body cap: 50 MiB.",
                "requestBody": {
                    "required": True,
                    "content": {"multipart/form-data": {"schema": {"type": "object", "properties": {"file": {"type": "string", "format": "binary", "description": "The skill zip."}}, "required": ["file"]}}},
                },
                "parameters": [
                    {"name": "channel", "in": "query", "schema": {"type": "string", "enum": ["stable", "beta", "edge", "internal"], "default": "stable"}},
                ],
                "responses": {
                    "201": {"description": "Created.", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/SkillVersion"}}}},
                    "400": err("BadRequest"),
                    "401": err("Unauthorized"),
                    "409": err("Conflict"),
                    "413": err("PayloadTooLarge"),
                    "415": err("UnsupportedMediaType"),
                },
            },
        },
        "/api/v1/skills/{source}": {
            "get": {
                "tags": ["skills"],
                "summary": "List all skills under one source.",
                "parameters": [{"$ref": "#/components/parameters/SkillSource"}],
                "responses": {
                    "200": ok_json("IndexStripped"),
                    "401": err("Unauthorized"),
                    "404": err("NotFound"),
                    "503": err("ServiceUnavailable"),
                },
            },
        },
        "/api/v1/skills/{source}/{name}": {
            "get": {
                "tags": ["skills"],
                "summary": "Get one skill (all versions, full detail).",
                "parameters": [{"$ref": "#/components/parameters/SkillSource"}, {"$ref": "#/components/parameters/SkillName"}],
                "responses": {
                    "200": ok_json("Skill"),
                    "401": err("Unauthorized"),
                    "404": err("NotFound"),
                    "503": err("ServiceUnavailable"),
                },
            },
            "delete": {
                "tags": ["skills"],
                "summary": "Delete every version of a skill under one source.",
                "parameters": [{"$ref": "#/components/parameters/SkillSource"}, {"$ref": "#/components/parameters/SkillName"}],
                "responses": {
                    "200": {"description": "OK.", "content": {"application/json": {"schema": delete_all_responses_schema}}},
                    "401": err("Unauthorized"),
                    "404": err("NotFound"),
                },
            },
        },
        "/api/v1/skills/{source}/{name}/{version}": {
            "get": {
                "tags": ["skills"],
                "summary": "Get one skill version (full detail).",
                "parameters": [{"$ref": "#/components/parameters/SkillSource"}, {"$ref": "#/components/parameters/SkillName"}, {"$ref": "#/components/parameters/SkillVersion"}],
                "responses": {
                    "200": ok_json("SkillVersion"),
                    "401": err("Unauthorized"),
                    "404": err("NotFound"),
                    "503": err("ServiceUnavailable"),
                },
            },
            "delete": {
                "tags": ["skills"],
                "summary": "Delete one (source, name, version) entry.",
                "parameters": [{"$ref": "#/components/parameters/SkillSource"}, {"$ref": "#/components/parameters/SkillName"}, {"$ref": "#/components/parameters/SkillVersion"}],
                "responses": {
                    "200": {"description": "OK.", "content": {"application/json": {"schema": delete_single_responses_schema}}},
                    "401": err("Unauthorized"),
                    "404": err("NotFound"),
                },
            },
        },
        "/api/v1/skills/{source}/{name}/{version}/download": {
            "get": {
                "tags": ["skills"],
                "summary": "Stream the skill zip bytes (application/zip).",
                "parameters": [{"$ref": "#/components/parameters/SkillSource"}, {"$ref": "#/components/parameters/SkillName"}, {"$ref": "#/components/parameters/SkillVersion"}],
                "responses": {
                    "200": ok_zip(),
                    "401": err("Unauthorized"),
                    "404": err("NotFound"),
                    "503": err("ServiceUnavailable"),
                },
            },
        },
        "/api/v1/skills/{source}/{name}/{version}/sha256": {
            "get": {
                "tags": ["skills"],
                "summary": "sha256 sidecar text (<hex>  <filename>\\n) for sha256sum(1) verification.",
                "parameters": [{"$ref": "#/components/parameters/SkillSource"}, {"$ref": "#/components/parameters/SkillName"}, {"$ref": "#/components/parameters/SkillVersion"}],
                "responses": {
                    "200": ok_text("text/plain"),
                    "401": err("Unauthorized"),
                    "404": err("NotFound"),
                    "503": err("ServiceUnavailable"),
                },
            },
        },
        "/api/v1/skills/{source}/{name}/{version}/SKILL.md": {
            "get": {
                "tags": ["skills"],
                "summary": "Raw SKILL.md (frontmatter + body) read byte-identical from the zip.",
                "description": "Distinct from SkillVersion.Body which is parsed YAML+Markdown; this endpoint serves the original bytes for diff tools.",
                "parameters": [{"$ref": "#/components/parameters/SkillSource"}, {"$ref": "#/components/parameters/SkillName"}, {"$ref": "#/components/parameters/SkillVersion"}],
                "responses": {
                    "200": ok_text("text/markdown"),
                    "401": err("Unauthorized"),
                    "404": err("NotFound"),
                    "503": err("ServiceUnavailable"),
                },
            },
        },
        "/api/v1/skills-index.json": {
            "get": {
                "tags": ["skills"],
                "summary": "Raw dist/skills-index.json (debug).",
                "responses": {
                    "200": ok_json("Index"),
                    "401": err("Unauthorized"),
                    "503": err("ServiceUnavailable"),
                },
            },
        },
    }
    spec["paths"].update(skill_paths)

    text = json.dumps(spec, indent=2, ensure_ascii=False)
    DOCS.write_text(text)
    print(f"Wrote {DOCS} ({len(text)} bytes, {len(spec['paths'])} paths, {len(spec['components']['schemas'])} schemas)")


if __name__ == "__main__":
    main()