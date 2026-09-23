package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/go-chi/chi/v5"
)

// HandleSkillsList returns the (stripped) skills index. The response is
// the parallel of HandleIndex for agents.
//
// Query parameters:
//   - source=community|internal  — filter to a single source; omitted
//     returns skills from all sources interleaved by name.
//   - channel=stable|beta|edge|internal|latest — keep only each skill's
//     latest version in the given channel (latest=stable alias, npm-style).
//     Default: stable.
//
// 503 if the skills index hasn't been loaded yet (initial startup race).
func HandleSkillsList(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.SkillsIndex() == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "skills-index.json not loaded yet")
			return
		}
		idx := s.SkillsIndex()
		sourceFilter := r.URL.Query().Get("source")
		channelFilter := r.URL.Query().Get("channel")

		stripped := idx.Strip()
		// Apply source filter (cheap — re-build the slice).
		if sourceFilter != "" {
			if !IsValidSource(sourceFilter) {
				writeError(w, http.StatusNotFound, "not_found", "unknown source: "+sourceFilter)
				return
			}
			filtered := stripped.Skills[:0]
			for _, sk := range stripped.Skills {
				// Match source against ANY version's source field (a skill
				// could theoretically have versions in multiple sources in
				// future — but in v1 the storage key is (name, source)).
				for _, v := range sk.Versions {
					if v.Source == sourceFilter {
						filtered = append(filtered, sk)
						break
					}
				}
			}
			stripped.Skills = filtered
		}
		// Apply channel filter (server-side projection).
		if channelFilter != "" {
			stripped = stripped.FilterByChannel(channelFilter)
		}
		writeJSON(w, http.StatusOK, stripped)
	}
}

// HandleSkillSourceList returns the (stripped) skills index filtered to
// exactly one source. Mirrors HandleSkillsList with ?source=X but URL
// path is the cleaner form /skills/{source}.
func HandleSkillSourceList(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.SkillsIndex() == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "skills-index.json not loaded yet")
			return
		}
		source := chi.URLParam(r, "source")
		if !IsValidSource(source) {
			writeError(w, http.StatusNotFound, "not_found", "unknown source: "+source)
			return
		}
		stripped := s.SkillsIndex().Strip()
		filtered := stripped.Skills[:0]
		for _, sk := range stripped.Skills {
			for _, v := range sk.Versions {
				if v.Source == source {
					filtered = append(filtered, sk)
					break
				}
			}
		}
		stripped.Skills = filtered
		// Apply channel filter if requested.
		if ch := r.URL.Query().Get("channel"); ch != "" {
			stripped = stripped.FilterByChannel(ch)
		}
		writeJSON(w, http.StatusOK, stripped)
	}
}

// HandleSkill returns one skill's full metadata + all versions under the
// given source. Mirrors HandleAgent for agents.
func HandleSkill(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.SkillsIndex() == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "skills-index.json not loaded yet")
			return
		}
		source := chi.URLParam(r, "source")
		name := chi.URLParam(r, "name")
		if !IsValidSource(source) {
			writeError(w, http.StatusNotFound, "not_found", "unknown source: "+source)
			return
		}
		for _, sk := range s.SkillsIndex().Skills {
			if sk.Name != name {
				continue
			}
			// Match if any version has the requested source.
			for _, v := range sk.Versions {
				if v.Source == source {
					writeJSON(w, http.StatusOK, sk)
					return
				}
			}
			break
		}
		writeError(w, http.StatusNotFound, "not_found",
			"skill "+name+" not found in source "+source)
	}
}

// HandleSkillVersion returns the full SkillVersion for one
// (source, name, version) — the parallel of HandleManifest for agents.
func HandleSkillVersion(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.SkillsIndex() == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "skills-index.json not loaded yet")
			return
		}
		source := chi.URLParam(r, "source")
		name := chi.URLParam(r, "name")
		version := chi.URLParam(r, "version")
		if !IsValidSource(source) {
			writeError(w, http.StatusNotFound, "not_found", "unknown source: "+source)
			return
		}
		for _, sk := range s.SkillsIndex().Skills {
			if sk.Name != name {
				continue
			}
			for _, v := range sk.Versions {
				if v.Source == source && v.Version == version {
					writeJSON(w, http.StatusOK, v)
					return
				}
			}
			break
		}
		writeError(w, http.StatusNotFound, "not_found",
			"skill version not found: "+source+"/"+name+"/"+version)
	}
}

// HandleSkillDownload streams the zip bytes for one (source, name, version).
// Parallel of HandleTarball.
func HandleSkillDownload(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.SkillsIndex() == nil || s.Dist == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "skills-index.json not loaded yet")
			return
		}
		source := chi.URLParam(r, "source")
		name := chi.URLParam(r, "name")
		version := chi.URLParam(r, "version")
		if !IsValidSource(source) {
			writeError(w, http.StatusNotFound, "not_found", "unknown source: "+source)
			return
		}
		zipRef, ok := findSkillZip(s, source, name, version)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found",
				"skill zip not found: "+source+"/"+name+"/"+version)
			return
		}
		path := s.Dist.SkillZipPath(zipRef.Filename)
		// Sanity-check the file exists on disk before serving — index
		// could reference a zip that's missing (corrupted deployment).
		if _, err := os.Stat(path); err != nil {
			writeError(w, http.StatusNotFound, "not_found",
				"skill zip on disk: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+zipRef.Filename+`"`)
		http.ServeFile(w, r, path)
	}
}

// HandleSkillSHA256 returns the sha256 sidecar text for one skill zip.
// Format matches sha256sum: "<hex>  <filename>\n".
func HandleSkillSHA256(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.SkillsIndex() == nil || s.Dist == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "skills-index.json not loaded yet")
			return
		}
		source := chi.URLParam(r, "source")
		name := chi.URLParam(r, "name")
		version := chi.URLParam(r, "version")
		if !IsValidSource(source) {
			writeError(w, http.StatusNotFound, "not_found", "unknown source: "+source)
			return
		}
		zipRef, ok := findSkillZip(s, source, name, version)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found",
				"skill not found: "+source+"/"+name+"/"+version)
			return
		}
		data, err := os.ReadFile(s.Dist.SkillSHA256Path(zipRef.Filename))
		if err != nil {
			if errorsIsNotExist(err) {
				writeError(w, http.StatusNotFound, "not_found",
					"sha256 sidecar missing on disk")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error",
				"read sha256: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(data)
	}
}

// HandleSkillRawMD returns the canonical SKILL.md bytes (frontmatter +
// body) for one skill version. Comes from the zip on disk so the bytes
// are byte-identical to what the author uploaded — distinct from
// SkillVersion.Body which is parsed and re-serialized.
//
// The skill index Body is embedded for list/detail convenience, but the
// zip's SKILL.md is the authoritative copy. This endpoint serves that.
func HandleSkillRawMD(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.SkillsIndex() == nil || s.Dist == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "skills-index.json not loaded yet")
			return
		}
		source := chi.URLParam(r, "source")
		name := chi.URLParam(r, "name")
		version := chi.URLParam(r, "version")
		if !IsValidSource(source) {
			writeError(w, http.StatusNotFound, "not_found", "unknown source: "+source)
			return
		}
		zipRef, ok := findSkillZip(s, source, name, version)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found",
				"skill not found: "+source+"/"+name+"/"+version)
			return
		}
		zipPath := s.Dist.SkillZipPath(zipRef.Filename)
		m, raw, err := skills.ExtractSkillMD(zipPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error",
				"extract SKILL.md: "+err.Error())
			return
		}
		// Defensive: ensure the SKILL.md we extracted is for the version
		// the caller asked for (would indicate index/zip mismatch).
		if m.Name != name || m.Version != version {
			writeError(w, http.StatusInternalServerError, "index_zip_mismatch",
				"SKILL.md name/version does not match the index entry")
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write(raw)
	}
}

// HandleRawSkillsIndex serves dist/skills-index.json verbatim from the
// in-memory state. Parallel of HandleRawIndex for agents.
func HandleRawSkillsIndex(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.SkillsIndex() == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "skills-index.json not loaded yet")
			return
		}
		writeJSON(w, http.StatusOK, s.SkillsIndex())
	}
}

// findSkillZip returns the (filename, sha256) for a single (source, name,
// version) skill. Mirrors findTarball for agents.
func findSkillZip(s *State, source, name, version string) (skills.Zip, bool) {
	for _, sk := range s.SkillsIndex().Skills {
		if sk.Name != name {
			continue
		}
		for _, v := range sk.Versions {
			if v.Source == source && v.Version == version {
				return v.Zip, true
			}
		}
		break
	}
	return skills.Zip{}, false
}

// trimPathSegment is a tiny helper used by handlers that read path
// segments to ensure callers can't smuggle in characters that confuse
// downstream URL parsers. Already validated by chi routing, but cheap
// defense in depth.
func trimPathSegment(s string) string {
	return strings.TrimSpace(s)
}

// skillUploadMaxBytes caps a single skill zip upload. Matches the limit
// documented in the Phase 6 plan section §4.2.
const skillUploadMaxBytes = 50 << 20 // 50 MiB

// HandleSkillUpload accepts a multipart POST with a "file" field carrying
// the skill zip, validates SKILL.md, persists the zip + sidecar to
// dist/skills/, rebuilds dist/skills-index.json, and atomically publishes
// the new index. The whole critical section is serialized through
// State.SkillsDir.Lock so concurrent uploads (or a concurrent reload-mutating
// write) can't tear the index.
//
// Status codes (mirrors §4.1 of the plan):
//   - 201 Created       → success, body = the new SkillVersion JSON
//   - 400 Bad Request   → SKILL.md missing, validation error, name/version
//                         mismatch with filename
//   - 401 Unauthorized  → handled by BasicAuthMiddleware
//   - 409 Conflict      → (name, source, version) already exists
//   - 413 Payload Too Large → body > 50 MiB
//   - 415 Unsupported Media Type → not multipart/form-data
//   - 503 Service Unavailable → State.Dist not configured (Dist nil)
func HandleSkillUpload(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Dist == nil || s.SkillsDir == nil {
			writeError(w, http.StatusServiceUnavailable, "no_dist", "dist directory not configured")
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			writeError(w, http.StatusUnsupportedMediaType, "bad_content_type",
				"POST must use multipart/form-data with a 'file' field")
			return
		}
		// Cap body size before parsing multipart — anything larger than
		// 50 MiB returns 413 without ever landing on disk.
		r.Body = http.MaxBytesReader(w, r.Body, skillUploadMaxBytes)
		// 32 MB part buffer: multipart.ParseMultipartForm preloads the
		// whole form into memory or a temp file. 32 MB is large enough
		// for the zip's central directory (typically < 1 MB) but keeps
		// the in-memory ceiling predictable.
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			var mbErr *http.MaxBytesError
			if errors.As(err, &mbErr) {
				writeError(w, http.StatusRequestEntityTooLarge, "too_large",
					fmt.Sprintf("skill zip exceeds %d bytes", skillUploadMaxBytes))
				return
			}
			writeError(w, http.StatusBadRequest, "bad_multipart", err.Error())
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing_file",
				"multipart 'file' field required: "+err.Error())
			return
		}
		defer file.Close()
		zipName := filepath.Base(header.Filename)
		name, fileSource, fileVer, err := skills.ParseZipFilename(zipName)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_filename",
				"uploaded zip filename is not <name>[-<source>]-<version>.zip: "+err.Error())
			return
		}
		// Channel is provided as a query parameter (?channel=stable|...).
		// Default stable; unknown values are rejected (no implicit
		// fallback that could mask typos).
		channel := r.URL.Query().Get("channel")
		if channel == "" {
			channel = "stable"
		}
		if !skills.IsValidChannel(channel) {
			writeError(w, http.StatusBadRequest, "bad_channel",
				"unknown channel: "+channel)
			return
		}
		// Resolve `source`: filename-encoded wins if it's a valid source
		// (CLI build output), else fall back to the multipart `source`
		// form field (console uploads), else default "community". Source
		// is intentionally NOT a structural part of the zip filename
		// anymore — the filename is a blob name; provenance lives here.
		source := fileSource
		if !skills.IsValidSource(source) {
			source = r.PostFormValue("source")
		}
		if !skills.IsValidSource(source) {
			source = "community"
		}
		// Serialize the write — read-endpoints use atomic.Pointer, but
		// the index file itself (skills-index.json) and .tmp/ zip
		// staging are filesystem state that needs explicit ordering.
		s.SkillsDir.Lock()
		defer s.SkillsDir.Unlock()

		// Stage upload to a tmp path under dist/skills/.tmp/. We use the
		// raw bytes here (no streaming sha256) so that the in-tmp hash
		// matches what we then commit to disk.
		tmpDir, err := s.SkillsDir.EnsureTmpDir()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error",
				"ensure tmp dir: "+err.Error())
			return
		}
		tmp, err := os.CreateTemp(tmpDir, ".upload-*")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error",
				"create tmp: "+err.Error())
			return
		}
		tmpName := tmp.Name()
		defer func() {
			// Best-effort cleanup if anything below errors. Without this,
			// a failed upload leaves a .upload-* in .tmp/ that the
			// reload loop would otherwise skip over.
			_ = os.Remove(tmpName)
		}()
		written, err := io.Copy(tmp, file)
		if err != nil {
			tmp.Close()
			writeError(w, http.StatusBadRequest, "read_body",
				"read upload body: "+err.Error())
			return
		}
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			writeError(w, http.StatusInternalServerError, "internal_error",
				"fsync tmp: "+err.Error())
			return
		}
		if err := tmp.Close(); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error",
				"close tmp: "+err.Error())
			return
		}

		// Validate zip + SKILL.md before any rename.
		mfst, rawMD, err := skills.ExtractSkillMD(tmpName)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_skill_zip",
				"zip/SKILL.md invalid: "+err.Error())
			return
		}
		if err := mfst.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_manifest",
				"SKILL.md validation: "+err.Error())
			return
		}
		// Resolve `version`: from the filename when present; otherwise
		// SKILL.md is the authoritative source (the operator may have
		// shipped a zip named just "<name>.zip" with version only in the
		// manifest).
		version := fileVer
		if version == "" {
			version = mfst.Version
		}
		// Name in zip must match SKILL.md. The zip filename's name segment
		// is always present (ParseZipFilename rejects empty names), so a
		// mismatch here means the operator uploaded the wrong zip.
		if mfst.Name != name {
			writeError(w, http.StatusBadRequest, "name_mismatch",
				fmt.Sprintf("zip filename says name=%q but SKILL.md says name=%q", name, mfst.Name))
			return
		}
		// Version mismatch only checked when the filename carried a
		// version segment — a 1-segment filename ("<name>.zip") defers
		// to SKILL.md for the version.
		if fileVer != "" && mfst.Version != fileVer {
			writeError(w, http.StatusBadRequest, "version_mismatch",
				fmt.Sprintf("zip filename says version=%q but SKILL.md says version=%q", fileVer, mfst.Version))
			return
		}
		// Canonical on-disk filename. zipName (the operator-provided
		// name) and finalZipName can differ when the operator uploaded
		// a 1- or 2-segment filename — server canonicalises so the
		// dist/skills/ layout stays uniform regardless of how the
		// community zip was named.
		finalZipName := fmt.Sprintf("%s-%s-%s.zip", name, source, version)

		// Compute sha256 from the on-disk tmp file (post-Sync, so what
		// we'll commit matches what we hashed).
		hexSum, err := skills.Sha256File(tmpName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error",
				"hash tmp: "+err.Error())
			return
		}

		// Immutability check BEFORE the rename: if (name, source, version)
		// already exists in the index, reject with 409 without touching
		// dist/skills/.
		existing := s.SkillsIndex()
		if existing != nil {
			if sk, ok := existing.FindSkill(name); ok {
				if _, ok := sk.FindVersion(source, version); ok {
					writeError(w, http.StatusConflict, "already_exists",
						fmt.Sprintf("%s/%s/%s already exists in registry", source, name, version))
					return
				}
			}
		}

		// Commit: rename tmp → final, write sidecar, rebuild index. The on-disk
		// filename is always the canonical "<name>-<source>-<version>.zip"
		// — even when the operator uploaded a 1- or 2-segment filename
		// like "ppt-maker-1.0.3.zip" or "ppt-maker.zip".
		finalPath := s.SkillsDir.ZipPath(finalZipName)
		if err := os.Rename(tmpName, finalPath); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error",
				"rename tmp: "+err.Error())
			return
		}
		// Sidecar — sha256sum(1) format "<hex>  \n" (two spaces,
		// matching the rest of the marketplace).
		sidecarPath := s.SkillsDir.SHA256Path(finalZipName)
		sidecar := []byte(hexSum + "  " + finalZipName + "\n")
		if err := os.WriteFile(sidecarPath, sidecar, 0o644); err != nil {
			// Roll back the rename to keep disk state consistent.
			_ = os.Remove(finalPath)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"write sidecar: "+err.Error())
			return
		}

		// Rebuild the index: load current, append the new SkillVersion,
		// marshal, atomic-write.
		sv := skills.SkillVersion{
			Version:    version,
			Source:     source,
			Channel:    channel,
			ReleasedAt: time.Now().UTC().Format(time.RFC3339),
			Requires:   requiresPointer(mfst.Metadata.Requires),
			Inputs:     mfst.Metadata.Inputs,
			EntryPoint: mfst.Metadata.EntryPoint,
			// Schema v2.0 fields — copied verbatim from the parsed manifest.
			// Agents / Category are required by Validate, so non-empty here.
			Agents:        append([]string(nil), mfst.Agents...),
			InstallMethod: mfst.Metadata.InstallMethod,
			InstallPaths:  copyStringMap(mfst.Metadata.InstallPaths),
			InstallConfig: copyAnyMap(mfst.Metadata.InstallConfig),
			Zip: skills.Zip{
				Filename:  finalZipName,
				SizeBytes: written,
				SHA256:    "sha256:" + hexSum,
			},
			Body: string(rawMD),
		}
		newIdx, err := upsertSkillInIndex(existing, name, sv, mfst)
		if err != nil {
			s.SkillsDir.RemoveZip(finalZipName)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"rebuild index: "+err.Error())
			return
		}
		idxBytes, err := skills.MarshalIndex(newIdx)
		if err != nil {
			s.SkillsDir.RemoveZip(finalZipName)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"marshal index: "+err.Error())
			return
		}
		idxPath := s.SkillsDir.IndexPath()
		if err := writeFileAtomic(idxPath, idxBytes, 0o644); err != nil {
			s.SkillsDir.RemoveZip(finalZipName)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"write index: "+err.Error())
			return
		}
		s.SetSkillsIndex(newIdx)

		writeJSON(w, http.StatusCreated, sv)
	}
}

// upsertSkillInIndex returns a new *skills.Index with the given SkillVersion
// added under name. Skill-level fields are populated from mfst on first
// add (when no Skill with that name exists in existing); subsequent
// versions under the same name inherit the existing skill-level metadata.
//
// Returns the input index unchanged if it was nil — defensive against a
// race with the initial reload on first start.
func upsertSkillInIndex(existing *skills.Index, name string, sv skills.SkillVersion, mfst *skills.Manifest) (*skills.Index, error) {
	idx := existing
	if idx == nil {
		idx = &skills.Index{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			Schema:      skills.IndexSchemaVersion,
		}
	}
	idx.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	if idx.Schema == "" {
		idx.Schema = skills.IndexSchemaVersion
	}
	// Deep copy the skill slice so the published index doesn't share
	// backing arrays with the prior one (the index is supposed to be
	// immutable once published).
	skillsCopy := make([]skills.Skill, len(idx.Skills))
	copy(skillsCopy, idx.Skills)

	added := false
	for i := range skillsCopy {
		if skillsCopy[i].Name != name {
			continue
		}
		// Append a copy of the new version and sort.
		vCopy := sv
		skillsCopy[i].Versions = append(skillsCopy[i].Versions, vCopy)
		skillsCopy[i].SortVersions()
		// Fill in any missing skill-level metadata from the manifest.
		if skillsCopy[i].DisplayName == "" {
			skillsCopy[i].DisplayName = titleCase(name)
		}
		if skillsCopy[i].Author == "" && mfst.Author != "" {
			skillsCopy[i].Author = mfst.Author
		}
		if skillsCopy[i].License == "" && mfst.License != "" {
			skillsCopy[i].License = mfst.License
		}
		if skillsCopy[i].Homepage == "" && mfst.Homepage != "" {
			skillsCopy[i].Homepage = mfst.Homepage
		}
		if skillsCopy[i].CreatedAt == "" && mfst.CreatedAt != "" {
			skillsCopy[i].CreatedAt = mfst.CreatedAt
		}
		if len(skillsCopy[i].Tags) == 0 && len(mfst.Tags) > 0 {
			skillsCopy[i].Tags = append([]string(nil), mfst.Tags...)
		}
		if skillsCopy[i].Description == "" {
			skillsCopy[i].Description = mfst.Description
		}
		// Schema v2.0: Category is required, so backfill on first sight.
		if skillsCopy[i].Category == "" && mfst.Category != "" {
			skillsCopy[i].Category = mfst.Category
		}
		added = true
		break
	}
	if !added {
		skillsCopy = append(skillsCopy, skills.Skill{
			Name:        name,
			DisplayName: titleCase(name),
			Description: mfst.Description,
			Author:      mfst.Author,
			Tags:        append([]string(nil), mfst.Tags...),
			License:     mfst.License,
			Homepage:    mfst.Homepage,
			CreatedAt:   mfst.CreatedAt,
			Category:    mfst.Category,
			Versions:    []skills.SkillVersion{sv},
		})
	}
	idx.Skills = skillsCopy
	return idx, nil
}

// requiresPointer turns a value-type Requires into a pointer so it can be
// omitted from JSON when empty (via *Requires `json:",omitempty"`). Returns
// nil for an empty Requires (no os/arch/tools set), so the field is dropped
// from the marshalled JSON entirely.
func requiresPointer(r skills.Requires) *skills.Requires {
	if r.OS == nil && r.Arch == nil && r.Tools == nil {
		return nil
	}
	cp := r
	return &cp
}

// titleCase is a tiny local helper that capitalizes the first character
// (a-z only) of a skill name. Mirrors skillscmd.titleCase to keep the
// registry's DisplayName generation consistent with `agentpkg skills build`.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	first := s[0]
	if first >= 'a' && first <= 'z' {
		return string(first-32) + s[1:]
	}
	return s
}

// HandleSkillDelete removes skill zip(s) from the registry. Two URL
// shapes map to two modes:
//
//   - DELETE /api/v1/skills/{source}/{name}/{version}
//     Removes one specific (source, name, version) entry: zip + sidecar
//     + the matching SkillVersion under Skill.Versions. Returns 200
//     {"deleted": "<filename>"}.
//
//   - DELETE /api/v1/skills/{source}/{name}
//     Removes every version under that (source, name) pair. Returns
//     200 {"deleted": ["file1.zip", ...]}.
//
// Idempotency: missing-on-disk entries are logged at warn level and
// skipped (a partial rebuild after a crash should still succeed). A
// request for a (source, name, version) that the index doesn't know
// about returns 404.
//
// The whole critical section is serialized through State.SkillsDir.Lock
// so a delete + a concurrent upload (or reload-mutating write) can't
// tear dist/skills-index.json.
//
// Status codes (mirrors §4.1 of the plan):
//   - 200 OK          → success
//   - 401 Unauthorized → handled by BasicAuthMiddleware
//   - 404 Not Found   → index has no matching entry
func HandleSkillDelete(s *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Dist == nil || s.SkillsDir == nil {
			writeError(w, http.StatusServiceUnavailable, "no_dist", "dist directory not configured")
			return
		}
		idx := s.SkillsIndex()
		if idx == nil {
			writeError(w, http.StatusServiceUnavailable, "no_index", "skills-index.json not loaded yet")
			return
		}
		source := chi.URLParam(r, "source")
		name := chi.URLParam(r, "name")
		version := chi.URLParam(r, "version") // empty for the all-versions endpoint
		if !IsValidSource(source) {
			writeError(w, http.StatusNotFound, "not_found", "unknown source: "+source)
			return
		}

		// Serialize writes against dist/skills/ — uploads + reload use
		// the same lock.
		s.SkillsDir.Lock()
		defer s.SkillsDir.Unlock()

		// Build the new skills slice. We rebuild from scratch rather
		// than mutating in place because the published index is
		// supposed to be immutable once SetSkillsIndex returns.
		newSkills := make([]skills.Skill, 0, len(idx.Skills))
		deleted := []string{}
		notFound := true

		for _, sk := range idx.Skills {
			if sk.Name != name {
				newSkills = append(newSkills, sk)
				continue
			}
			if version != "" {
				// Single-version mode: find the exact (source, version).
				if _, ok := sk.FindVersion(source, version); !ok {
					newSkills = append(newSkills, sk)
					continue
				}
				notFound = false
				// Remove zip + sidecar (best-effort: missing files OK).
				zipName := findZipFilename(sk, source, version)
				if zipName != "" {
					s.SkillsDir.RemoveZip(zipName)
					deleted = append(deleted, zipName)
				}
				// Filter the matching version out.
				kept := sk.Versions[:0]
				for _, v := range sk.Versions {
					if v.Source == source && v.Version == version {
						continue
					}
					kept = append(kept, v)
				}
				if len(kept) == 0 {
					// No versions left → drop the Skill entirely.
					continue
				}
				sk.Versions = kept
				newSkills = append(newSkills, sk)
				continue
			}
			// All-versions mode under this source.
			kept := sk.Versions[:0]
			for _, v := range sk.Versions {
				if v.Source == source {
					notFound = false
					s.SkillsDir.RemoveZip(v.Zip.Filename)
					deleted = append(deleted, v.Zip.Filename)
					continue
				}
				kept = append(kept, v)
			}
			if len(kept) == 0 {
				// Skill had only versions under this source — drop it.
				continue
			}
			sk.Versions = kept
			newSkills = append(newSkills, sk)
		}

		if notFound {
			writeError(w, http.StatusNotFound, "not_found",
				"no skill found for "+source+"/"+name+
					conditionalVersion(version))
			return
		}

		// Rebuild + publish.
		idx.Skills = newSkills
		idx.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
		idxBytes, err := skills.MarshalIndex(idx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error",
				"marshal index: "+err.Error())
			return
		}
		if err := writeFileAtomic(s.SkillsDir.IndexPath(), idxBytes, 0o644); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error",
				"write index: "+err.Error())
			return
		}
		s.SetSkillsIndex(idx)

		if version != "" {
			writeJSON(w, http.StatusOK, map[string]string{"deleted": deleted[0]})
			return
		}
		writeJSON(w, http.StatusOK, map[string][]string{"deleted": deleted})
	}
}

// findZipFilename returns the zip filename for the (source, version)
// entry on this Skill, or "" if absent. Free function (not a method on
// skills.Skill) so it can live in the server package without needing to
// extend the skills type.
func findZipFilename(sk skills.Skill, source, version string) string {
	for _, v := range sk.Versions {
		if v.Source == source && v.Version == version {
			return v.Zip.Filename
		}
	}
	return ""
}

func conditionalVersion(v string) string {
	if v == "" {
		return ""
	}
	return "/" + v
}

// copyStringMap returns a fresh map with the same entries; nil input
// returns nil so the JSON omitempty kicks in.
func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// copyAnyMap returns a fresh map with the same entries; nil input
// returns nil. Values are NOT deep-copied — callers must not mutate
// the originals after this returns. yaml.v3 unmarshal gives us
// map[string]interface{} with primitive values, so shallow copy is fine.
func copyAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
