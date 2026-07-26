package server

import (
	"net/http"

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
		if s.Index == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		writeJSON(w, http.StatusOK, s.Index.Strip())
	}
}

// HandleAgent returns one agent's full metadata + all versions (with manifests).
func HandleAgent(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Index == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		name := chi.URLParam(r, "name")
		for _, a := range s.Index.Agents {
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
		if s.Index == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		name := chi.URLParam(r, "name")
		source := chi.URLParam(r, "source")
		version := chi.URLParam(r, "version")
		for _, a := range s.Index.Agents {
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
		if s.Index == nil || s.Dist == nil {
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
		if s.Index == nil || s.Dist == nil {
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
		if s.Index == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "index.json not loaded yet")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// Re-serialize from in-memory state. (Could also just serve the file
		// from disk, but in-memory is consistent with what other handlers see.)
		writeJSON(w, http.StatusOK, s.Index)
	}
}

// findTarball looks up a single version entry across the index.
func findTarball(s *State, name, source, version string) (apitypes.TarballRef, bool) {
	for _, a := range s.Index.Agents {
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
