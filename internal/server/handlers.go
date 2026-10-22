package server

import (
	"net/http"
	"path"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/go-chi/chi/v5"
)

// HandleHealth reports server liveness.
// No auth required.
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": "0.1.0",
	})
}

// HandleIndex returns the full index with manifest fields stripped.
// Auth required.
func HandleIndex(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Index() == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		writeJSON(w, http.StatusOK, s.Index().Strip())
	}
}

// HandleAgent returns one agent's full metadata + all versions (with manifests).
func HandleAgent(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Index() == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		name := chi.URLParam(r, "name")
		for _, a := range s.Index().Agents {
			if a.Name == name {
				writeJSON(w, http.StatusOK, a)
				return
			}
		}
		writeError(w, http.StatusNotFound, "not_found", "agent "+name+" not found")
	}
}

// HandleManifest returns the full technical manifest.json for one (name, source, version).
func HandleManifest(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Index() == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		name := chi.URLParam(r, "name")
		source := chi.URLParam(r, "source")
		version := chi.URLParam(r, "version")
		for _, a := range s.Index().Agents {
			if a.Name != name {
				continue
			}
			for _, v := range a.Versions {
				if v.Source == source && v.Version == version {
					writeJSON(w, http.StatusOK, v.Manifest)
					return
				}
			}
		}
		writeError(w, http.StatusNotFound, "not_found",
			"manifest not found for "+name+"/"+source+"/"+version)
	}
}

// HandleTarball streams the tarball bytes for one (name, source, version).
func HandleTarball(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Index() == nil || s.Dist == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		name := chi.URLParam(r, "name")
		source := chi.URLParam(r, "source")
		version := chi.URLParam(r, "version")
		tarball, ok := findTarball(s, name, source, version)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found",
				"tarball not found for "+name+"/"+source+"/"+version)
			return
		}
		path := s.Dist.TarballPath(tarball.Filename)
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+tarball.Filename+`"`)
		http.ServeFile(w, r, path)
	}
}

// HandleSHA256 returns the sha256 sidecar file content for one tarball.
// Format matches `sha256sum` output: "<hex>  <filename>".
func HandleSHA256(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Index() == nil || s.Dist == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		name := chi.URLParam(r, "name")
		source := chi.URLParam(r, "source")
		version := chi.URLParam(r, "version")
		tarball, ok := findTarball(s, name, source, version)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found",
				"tarball not found for "+name+"/"+source+"/"+version)
			return
		}
		hex, err := s.Dist.ReadSHA256(tarball.Filename)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error",
				"read sha256: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(hex + "  " + tarball.Filename + "\n"))
	}
}

// HandleRawIndex serves dist/index.json verbatim. Useful for debugging
// or for clients that want to skip manifest-stripping.
func HandleRawIndex(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Index() == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// Re-serialize from in-memory state. (Could also just serve the file
		// from disk, but in-memory is consistent with what other handlers see.)
		writeJSON(w, http.StatusOK, s.Index())
	}
}

// HandleConfigSchema projects just the admin-layer config schema for one
// (name, source, version) — only what a daemon needs to construct a
// `config generate` payload (required_inputs + optional_inputs +
// supported_providers). Skips payload/scripts/checksums/services/
// upgrade/upstream that the full /manifest endpoint carries.
//
// The response shape mirrors ConfigSpec verbatim, wrapped under a
// top-level "agent" envelope (so callers can identify the source version
// without a separate /manifest call).
func HandleConfigSchema(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Index() == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		name := chi.URLParam(r, "name")
		source := chi.URLParam(r, "source")
		version := chi.URLParam(r, "version")
		for _, a := range s.Index().Agents {
			if a.Name != name {
				continue
			}
			for _, v := range a.Versions {
				if v.Source == source && v.Version == version {
					writeJSON(w, http.StatusOK, map[string]any{
						"agent":   name,
						"source":  source,
						"version": version,
						"configs": v.Manifest.Configs,
					})
					return
				}
			}
			break
		}
		writeError(w, http.StatusNotFound, "not_found",
			"agent/source/version not found: "+name+"/"+source+"/"+version)
	}
}

// findTarball looks up a single version entry across the index.
func findTarball(s *State, name, source, version string) (apitypes.TarballRef, bool) {
	for _, a := range s.Index().Agents {
		if a.Name != name {
			continue
		}
		for _, v := range a.Versions {
			if v.Source == source && v.Version == version {
				return apitypes.TarballRef{
					Filename: v.Tarball.Filename,
					SHA256:   v.Tarball.SHA256,
				}, true
			}
		}
	}
	return apitypes.TarballRef{}, false
}

// findVersionConfig returns the index's ConfigFiles entry for one
// (name, source, version), and the tarball filename the staged files are keyed
// by. ok is false when the version is unknown OR when it ships no config/
// directory (which is a legitimate state — the caller renders a 404 with a
// distinct code so the client can tell "no such agent version" from "this
// version has no schema").
func findVersionConfig(s *State, name, source, version string) (apitypes.ConfigFiles, string, bool, bool) {
	for _, a := range s.Index().Agents {
		if a.Name != name {
			continue
		}
		for _, v := range a.Versions {
			if v.Source != source || v.Version != version {
				continue
			}
			tarName := v.Tarball.Filename
			if v.Config == nil {
				return apitypes.ConfigFiles{}, tarName, false, true
			}
			return *v.Config, tarName, true, true
		}
		break
	}
	return apitypes.ConfigFiles{}, "", false, false
}

// HandleConfigSchemaFile serves the agent's config/schema.json — the draft-07
// JSON Schema describing the shape of every config slot. This is what the
// deploy wizard's form renderer consumes; the schema's top-level keys are the
// slot names and must match manifest configs[].name.
func HandleConfigSchemaFile(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Index() == nil || s.Dist == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		name := chi.URLParam(r, "name")
		source := chi.URLParam(r, "source")
		version := chi.URLParam(r, "version")
		cfgs, tarName, hasConfig, found := findVersionConfig(s, name, source, version)
		if !found {
			writeError(w, http.StatusNotFound, "not_found",
				"agent/source/version not found: "+name+"/"+source+"/"+version)
			return
		}
		if !hasConfig || cfgs.Schema == "" {
			writeError(w, http.StatusNotFound, "no_config_schema",
				"no config/schema.json for "+name+"/"+source+"/"+version+
					" (config status: "+configStatusOrMissing(cfgs, hasConfig)+")")
			return
		}
		path := s.Dist.ConfigFilePath(tarName, "schema.json")
		w.Header().Set("Content-Type", "application/json")
		http.ServeFile(w, r, path)
	}
}

// HandleConfigExampleFile serves the agent's config/example.* verbatim, in
// whatever format it was authored — opencode/openclaw ship example.json,
// hermes-agent ships example.yaml. The frontend renders it as-is in its raw
// editor, so the bytes must not be transformed or re-encoded.
func HandleConfigExampleFile(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Index() == nil || s.Dist == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		name := chi.URLParam(r, "name")
		source := chi.URLParam(r, "source")
		version := chi.URLParam(r, "version")
		cfgs, tarName, hasConfig, found := findVersionConfig(s, name, source, version)
		if !found {
			writeError(w, http.StatusNotFound, "not_found",
				"agent/source/version not found: "+name+"/"+source+"/"+version)
			return
		}
		if !hasConfig || cfgs.Example == "" {
			writeError(w, http.StatusNotFound, "no_config_example",
				"no config/example.* for "+name+"/"+source+"/"+version+
					" (config status: "+configStatusOrMissing(cfgs, hasConfig)+")")
			return
		}
		// Serve with the format the agent actually ships, so a raw editor can
		// treat it as YAML or JSON without sniffing. The filename is the
		// basename of the indexed path.
		filename := path.Base(cfgs.Example)
		contentType := "application/yaml"
		if strings.HasSuffix(filename, ".json") {
			contentType = "application/json"
		}
		w.Header().Set("Content-Type", contentType)
		http.ServeFile(w, r, s.Dist.ConfigFilePath(tarName, filename))
	}
}

// configStatusOrMissing renders the status string for an error message when
// the index entry is absent (version ships no config/ at all).
func configStatusOrMissing(cfgs apitypes.ConfigFiles, hasConfig bool) string {
	if !hasConfig {
		return apitypes.ConfigStatusMissing
	}
	if cfgs.Status == "" {
		return apitypes.ConfigStatusMissing
	}
	return cfgs.Status
}
