package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/repo"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/go-chi/chi/v5"
)

// chiCtx builds a minimal chi.Context with the given URL params so
// handler tests that call chi.URLParam() work without spinning up the
// full router. This is the pattern used in chi's own test suite.
func chiCtx(params map[string]string) context.Context {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
}

// skillFixture builds a small dist/ with one valid skill zip + matching
// skills-index.json. Returns the dist path + the *skills.Index that was
// loaded from it.
//
// zipName is "<name>-<source>-<version>.zip" (e.g. "hello-community-1.0.0.zip").
// zipBody is the raw content of SKILL.md that will be embedded in the zip.
func skillFixture(t *testing.T, name, source, version, zipBody string) (string, *skills.Index) {
	t.Helper()
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	zipName := name + "-" + source + "-" + version + ".zip"
	zipPath := filepath.Join(dist, "skills", zipName)

	// Build a real zip with archive/zip so ExtractSkillMD works.
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	fh := &zip.FileHeader{Name: "SKILL.md", Method: zip.Deflate}
	fh.SetMode(0o644)
	w, _ := zw.CreateHeader(fh)
	w.Write([]byte(zipBody))
	zw.Close()
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	// Compute sha256 via our package and write sidecar.
	hex, err := skills.Sha256File(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zipPath+".sha256",
		[]byte(hex+"  "+zipName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := &skills.Index{
		GeneratedAt: "2026-09-08T00:00:00Z",
		Schema:      skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{
				Name:        name,
				DisplayName: strings.Title(name),
				Description: "Test skill.",
				Author:      "Tester",
				Versions: []skills.SkillVersion{
					{
						Version: version,
						Source:  source,
						Channel: "stable",
						Zip: skills.Zip{
							Filename:  zipName,
							SizeBytes: int64(buf.Len()),
							SHA256:    "sha256:" + hex,
						},
						Body: "test body",
					},
				},
			},
		},
	}
	return dist, idx
}

func doGET(t *testing.T, h http.HandlerFunc, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// doDELETE builds a DELETE request with chi URL params injected (for
// handlers that call chi.URLParam). Equivalent to doGETWithParams but
// for DELETE.
func doDELETE(t *testing.T, h http.HandlerFunc, path string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil).WithContext(chiCtx(params))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// doGETWithParams does GET like doGET but also injects the given URL
// params into the request context (for chi.URLParam lookups).
func doGETWithParams(t *testing.T, h http.HandlerFunc, path string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(chiCtx(params))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// -----------------------------------------------------------------------
// List + filter endpoints
// -----------------------------------------------------------------------

func TestHandleSkillsList_OK(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "---\nname: hello\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	s := NewState(repo.NewDir(dist), nil, idx)

	rr := doGET(t, HandleSkillsList(s), "/api/v1/skills")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var body skills.IndexStripped
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Skills) != 1 || body.Skills[0].Name != "hello" {
		t.Errorf("unexpected skills: %+v", body.Skills)
	}
}

func TestHandleSkillsList_NoIndex(t *testing.T) {
	dist, _ := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, nil)
	rr := doGET(t, HandleSkillsList(s), "/api/v1/skills")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestHandleSkillsList_FilterBySource(t *testing.T) {
	dist := t.TempDir()
	idx := &skills.Index{
		Schema: skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{Name: "a", Description: "x", Versions: []skills.SkillVersion{
				{Version: "1.0.0", Source: "community", Channel: "stable", Zip: skills.Zip{Filename: "a.zip", SHA256: "sha256:abc"}},
			}},
			{Name: "b", Description: "y", Versions: []skills.SkillVersion{
				{Version: "1.0.0", Source: "internal", Channel: "stable", Zip: skills.Zip{Filename: "b.zip", SHA256: "sha256:def"}},
			}},
		},
	}
	s := NewState(repo.NewDir(dist), nil, idx)

	rr := doGET(t, HandleSkillsList(s), "/api/v1/skills?source=community")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body skills.IndexStripped
	json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Skills) != 1 || body.Skills[0].Name != "a" {
		t.Errorf("expected only community skill 'a', got %+v", body.Skills)
	}

	rr = doGET(t, HandleSkillsList(s), "/api/v1/skills?source=internal")
	json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Skills) != 1 || body.Skills[0].Name != "b" {
		t.Errorf("expected only internal skill 'b', got %+v", body.Skills)
	}

	// Invalid source → 404.
	rr = doGET(t, HandleSkillsList(s), "/api/v1/skills?source=evil")
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for invalid source, got %d", rr.Code)
	}
}

func TestHandleSkillsList_FilterByChannel(t *testing.T) {
	dist := t.TempDir()
	idx := &skills.Index{
		Schema: skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{Name: "x", Description: "x", Versions: []skills.SkillVersion{
				{Version: "1.0.0", Source: "community", Channel: "stable", Zip: skills.Zip{Filename: "x.zip", SHA256: "sha256:abc"}},
				{Version: "1.1.0", Source: "community", Channel: "beta", Zip: skills.Zip{Filename: "x.zip", SHA256: "sha256:abc"}},
			}},
		},
	}
	s := NewState(repo.NewDir(dist), nil, idx)

	// ?channel=stable → returns the 1.0.0 version.
	rr := doGET(t, HandleSkillsList(s), "/api/v1/skills?channel=stable")
	var body skills.IndexStripped
	json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Skills) != 1 || len(body.Skills[0].Versions) != 1 || body.Skills[0].Versions[0].Version != "1.0.0" {
		t.Errorf("stable filter wrong: %+v", body.Skills)
	}

	// ?channel=beta → returns the 1.1.0 version.
	rr = doGET(t, HandleSkillsList(s), "/api/v1/skills?channel=beta")
	json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Skills) != 1 || body.Skills[0].Versions[0].Version != "1.1.0" {
		t.Errorf("beta filter wrong: %+v", body.Skills)
	}
}

// -----------------------------------------------------------------------
// Per-source list endpoint
// -----------------------------------------------------------------------

func TestHandleSkillSourceList_OK(t *testing.T) {
	dist := t.TempDir()
	idx := &skills.Index{
		Schema: skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{Name: "a", Description: "x", Versions: []skills.SkillVersion{
				{Version: "1.0.0", Source: "community", Channel: "stable", Zip: skills.Zip{Filename: "a.zip"}},
			}},
			{Name: "b", Description: "y", Versions: []skills.SkillVersion{
				{Version: "1.0.0", Source: "internal", Channel: "stable", Zip: skills.Zip{Filename: "b.zip"}},
			}},
		},
	}
	s := NewState(repo.NewDir(dist), nil, idx)

	rr := doGETWithParams(t, HandleSkillSourceList(s), "/api/v1/skills/community",
		map[string]string{"source": "community"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body skills.IndexStripped
	json.Unmarshal(rr.Body.Bytes(), &body)
	if len(body.Skills) != 1 || body.Skills[0].Name != "a" {
		t.Errorf("expected 'a' only, got %+v", body.Skills)
	}

	// Invalid source in path → 404.
	rr = doGETWithParams(t, HandleSkillSourceList(s), "/api/v1/skills/evil",
		map[string]string{"source": "evil"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for invalid source, got %d", rr.Code)
	}
}// -----------------------------------------------------------------------
// Skill detail + version endpoints
// -----------------------------------------------------------------------

func TestHandleSkill_OK(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkill(s), "/api/v1/skills/community/hello",
		map[string]string{"source": "community", "name": "hello"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleSkill_NotFound(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkill(s), "/api/v1/skills/community/nonexistent",
		map[string]string{"source": "community", "name": "nonexistent"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestHandleSkill_NotInThisSource(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkill(s), "/api/v1/skills/internal/hello",
		map[string]string{"source": "internal", "name": "hello"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (skill exists in community, not internal)", rr.Code)
	}
}

func TestHandleSkill_InvalidSource(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkill(s), "/api/v1/skills/evil/hello",
		map[string]string{"source": "evil", "name": "hello"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for invalid source", rr.Code)
	}
}

func TestHandleSkillVersion_OK(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkillVersion(s), "/api/v1/skills/community/hello/1.0.0",
		map[string]string{"source": "community", "name": "hello", "version": "1.0.0"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var v skills.SkillVersion
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Version != "1.0.0" || v.Source != "community" {
		t.Errorf("version = %+v", v)
	}
}

func TestHandleSkillVersion_NotFound(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkillVersion(s), "/api/v1/skills/community/hello/9.9.9",
		map[string]string{"source": "community", "name": "hello", "version": "9.9.9"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d", rr.Code)
	}
}

// -----------------------------------------------------------------------
// Download / SHA256 / SKILL.md / raw index endpoints
// -----------------------------------------------------------------------

func TestHandleSkillDownload_OK(t *testing.T) {
	const skillMD = "---\nname: hello\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n"
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", skillMD)
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkillDownload(s), "/api/v1/skills/community/hello/1.0.0/download",
		map[string]string{"source": "community", "name": "hello", "version": "1.0.0"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q", got)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "hello-community-1.0.0.zip") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	// Body should be a valid zip.
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	if err != nil {
		t.Fatalf("response body is not a valid zip: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "SKILL.md" {
		t.Errorf("unexpected zip contents: %v", zr.File)
	}
}

func TestHandleSkillDownload_NotFound(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkillDownload(s), "/api/v1/skills/community/hello/9.9.9/download",
		map[string]string{"source": "community", "name": "hello", "version": "9.9.9"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestHandleSkillSHA256_OK(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkillSHA256(s), "/api/v1/skills/community/hello/1.0.0/sha256",
		map[string]string{"source": "community", "name": "hello", "version": "1.0.0"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.HasSuffix(body, "  hello-community-1.0.0.zip\n") {
		t.Errorf("sha256 body does not match expected format: %q", body)
	}
}

func TestHandleSkillSHA256_NotFound(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkillSHA256(s), "/api/v1/skills/community/hello/9.9.9/sha256",
		map[string]string{"source": "community", "name": "hello", "version": "9.9.9"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestHandleSkillRawMD_OK(t *testing.T) {
	const skillMD = "---\nname: hello\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\n# Body\n\ntext\n"
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", skillMD)
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkillRawMD(s), "/api/v1/skills/community/hello/1.0.0/SKILL.md",
		map[string]string{"source": "community", "name": "hello", "version": "1.0.0"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/markdown") {
		t.Errorf("Content-Type = %q", got)
	}
	if !strings.Contains(rr.Body.String(), "ok description") {
		t.Errorf("body missing frontmatter content: %s", rr.Body.String())
	}
}

func TestHandleSkillRawMD_NameMismatch(t *testing.T) {
	// Skill zip's SKILL.md says "foo" but the URL says "hello".
	const skillMD = "---\nname: foo\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n"
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", skillMD)
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGETWithParams(t, HandleSkillRawMD(s), "/api/v1/skills/community/hello/1.0.0/SKILL.md",
		map[string]string{"source": "community", "name": "hello", "version": "1.0.0"})
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (index/zip mismatch)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "index_zip_mismatch") {
		t.Errorf("error code missing: %s", rr.Body.String())
	}
}

func TestHandleRawSkillsIndex_OK(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	rr := doGET(t, HandleRawSkillsIndex(s), "/api/v1/skills-index.json")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got skills.Index
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "hello" {
		t.Errorf("raw index wrong: %+v", got)
	}
}

func TestHandleRawSkillsIndex_NoIndex(t *testing.T) {
	dist, _ := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, nil)
	rr := doGET(t, HandleRawSkillsIndex(s), "/api/v1/skills-index.json")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d", rr.Code)
	}
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func TestFindSkillZip_OK(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	zipRef, ok := findSkillZip(s, "community", "hello", "1.0.0")
	if !ok {
		t.Fatal("findSkillZip returned not-ok")
	}
	if zipRef.Filename != "hello-community-1.0.0.zip" {
		t.Errorf("filename = %q", zipRef.Filename)
	}
}

func TestFindSkillZip_NotFound(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0", "")
	s := NewState(repo.NewDir(dist), nil, idx)
	if _, ok := findSkillZip(s, "community", "hello", "9.9.9"); ok {
		t.Error("findSkillZip should return not-ok for missing version")
	}
	if _, ok := findSkillZip(s, "internal", "hello", "1.0.0"); ok {
		t.Error("findSkillZip should return not-ok for wrong source")
	}
	if _, ok := findSkillZip(s, "community", "other", "1.0.0"); ok {
		t.Error("findSkillZip should return not-ok for wrong name")
	}
}

func TestIsValidSource(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"community", true},
		{"internal", true},
		{"upstream", false},
		{"", false},
		{".", false},
		{"..", false},
		{"a/b", false},
		{"a\\b", false},
		{"community/../etc", false},
	}
	for _, tc := range cases {
		if got := IsValidSource(tc.s); got != tc.want {
			t.Errorf("IsValidSource(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := writeFileAtomic(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q", string(got))
	}
	// Tmp files should be cleaned up — no .tmp-* in the dir.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

// TestSafeOpen covers the helper used by the download handler. Other
// places where it's used are exercised via the handler tests.
func TestSafeOpen(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := safeOpen(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	if string(data) != "x" {
		t.Errorf("content = %q", string(data))
	}
	if _, err := safeOpen(filepath.Join(dir, "missing")); err == nil {
		t.Error("expected error for missing file")
	}
}

// -----------------------------------------------------------------------
// Upload (POST /api/v1/skills) — Phase 6
// -----------------------------------------------------------------------

// buildSkillZip builds a real, in-memory zip containing SKILL.md with the
// given frontmatter/body and returns the bytes. Mirrors the artifact the
// CLI's `agentpkg skills build` would produce.
func buildSkillZip(t *testing.T, skillMD string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	fh := &zip.FileHeader{Name: "SKILL.md", Method: zip.Deflate}
	fh.SetMode(0o644)
	w, err := zw.CreateHeader(fh)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(skillMD))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// doUpload performs a multipart POST against the upload handler and
// returns the recorder.
func doUpload(t *testing.T, h http.HandlerFunc, path, filename string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandleSkillUpload_OK(t *testing.T) {
	dist := t.TempDir()
	s := NewState(repo.NewDir(dist), nil, nil)

	zipBody := buildSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	rr := doUpload(t, HandleSkillUpload(s), "/api/v1/skills", "hello-community-1.0.0.zip", zipBody)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var sv skills.SkillVersion
	if err := json.Unmarshal(rr.Body.Bytes(), &sv); err != nil {
		t.Fatal(err)
	}
	if sv.Version != "1.0.0" || sv.Source != "community" || sv.Channel != "stable" {
		t.Errorf("unexpected sv: %+v", sv)
	}
	if sv.Zip.Filename != "hello-community-1.0.0.zip" {
		t.Errorf("zip filename = %q", sv.Zip.Filename)
	}
	if !strings.HasPrefix(sv.Zip.SHA256, "sha256:") {
		t.Errorf("sha256 prefix missing: %q", sv.Zip.SHA256)
	}
	// Files should exist on disk.
	if _, err := os.Stat(filepath.Join(dist, "skills", "hello-community-1.0.0.zip")); err != nil {
		t.Errorf("zip not on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dist, "skills", "hello-community-1.0.0.zip.sha256")); err != nil {
		t.Errorf("sidecar not on disk: %v", err)
	}
	// skills-index.json should have been (re)written.
	idxData, err := os.ReadFile(filepath.Join(dist, "skills-index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var idx skills.Index
	if err := json.Unmarshal(idxData, &idx); err != nil {
		t.Fatal(err)
	}
	if len(idx.Skills) != 1 || idx.Skills[0].Name != "hello" {
		t.Errorf("index skills = %+v", idx.Skills)
	}
	if len(idx.Skills[0].Versions) != 1 {
		t.Errorf("expected 1 version, got %d", len(idx.Skills[0].Versions))
	}
	// State should have published the index too.
	pubIdx := s.SkillsIndex()
	if pubIdx == nil || len(pubIdx.Skills) != 1 {
		t.Errorf("published index missing the new skill")
	}
}

func TestHandleSkillUpload_DuplicateIs409(t *testing.T) {
	dist := t.TempDir()
	// Pre-seed an index with one version.
	idx := &skills.Index{
		Schema: skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{Name: "hello", Description: "x", Versions: []skills.SkillVersion{
				{Version: "1.0.0", Source: "community", Channel: "stable",
					Zip: skills.Zip{Filename: "hello-community-1.0.0.zip", SHA256: "sha256:abc"}},
			}},
		},
	}
	s := NewState(repo.NewDir(dist), nil, idx)

	zipBody := buildSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	rr := doUpload(t, HandleSkillUpload(s), "/api/v1/skills", "hello-community-1.0.0.zip", zipBody)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleSkillUpload_NameMismatchIs400(t *testing.T) {
	dist := t.TempDir()
	s := NewState(repo.NewDir(dist), nil, nil)

	// Filename says "hello", SKILL.md says "goodbye".
	zipBody := buildSkillZip(t, "---\nname: goodbye\ndescription: Says goodbye to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	rr := doUpload(t, HandleSkillUpload(s), "/api/v1/skills", "hello-community-1.0.0.zip", zipBody)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "name_mismatch") {
		t.Errorf("expected name_mismatch code, got: %s", rr.Body.String())
	}
}

func TestHandleSkillUpload_BadChannelIs400(t *testing.T) {
	dist := t.TempDir()
	s := NewState(repo.NewDir(dist), nil, nil)

	zipBody := buildSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills?channel=evil", bytes.NewReader(buildMultipart(t, "hello-community-1.0.0.zip", zipBody)))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=---xxx")
	rr := httptest.NewRecorder()
	HandleSkillUpload(s).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleSkillUpload_NotMultipartIs415(t *testing.T) {
	dist := t.TempDir()
	s := NewState(repo.NewDir(dist), nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills", bytes.NewReader([]byte("not multipart")))
	req.Header.Set("Content-Type", "application/octet-stream")
	rr := httptest.NewRecorder()
	HandleSkillUpload(s).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rr.Code)
	}
}

// buildMultipart is a small helper used by tests that want to inspect the
// raw multipart wire format (e.g. when overriding Content-Type).
func buildMultipart(t *testing.T, filename string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	fw.Write(body)
	mw.Close()
	return buf.Bytes()
}

// -----------------------------------------------------------------------
// Delete (Phase 7) — DELETE /api/v1/skills/{source}/{name}/{version}
// -----------------------------------------------------------------------

func TestHandleSkillDelete_SingleVersion(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0",
		"---\nname: hello\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	s := NewState(repo.NewDir(dist), nil, idx)

	rr := doDELETE(t, HandleSkillDelete(s), "/api/v1/skills/community/hello/1.0.0",
		map[string]string{"source": "community", "name": "hello", "version": "1.0.0"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["deleted"] != "hello-community-1.0.0.zip" {
		t.Errorf("deleted = %q, want hello-community-1.0.0.zip", resp["deleted"])
	}
	// Zip + sidecar should be gone from disk.
	zipPath := filepath.Join(dist, "skills", "hello-community-1.0.0.zip")
	if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
		t.Errorf("expected zip to be removed: %v", err)
	}
	// Index should be empty (skill had only one version under that source).
	pub := s.SkillsIndex()
	if pub == nil || len(pub.Skills) != 0 {
		t.Errorf("expected empty index after delete, got %+v", pub)
	}
	// skills-index.json should reflect this.
	idxData, _ := os.ReadFile(filepath.Join(dist, "skills-index.json"))
	var persisted skills.Index
	json.Unmarshal(idxData, &persisted)
	if len(persisted.Skills) != 0 {
		t.Errorf("persisted index should be empty, got %+v", persisted.Skills)
	}
}

func TestHandleSkillDelete_KeepOtherVersions(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0",
		"---\nname: hello\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	// Add a second version under the same source.
	zipName := "hello-community-2.0.0.zip"
	zipPath := filepath.Join(dist, "skills", zipName)
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	fh := &zip.FileHeader{Name: "SKILL.md", Method: zip.Deflate}
	fh.SetMode(0o644)
	w, _ := zw.CreateHeader(fh)
	w.Write([]byte("---\nname: hello\ndescription: ok description\nversion: \"2.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n"))
	zw.Close()
	os.WriteFile(zipPath, buf.Bytes(), 0o644)
	hex, _ := skills.Sha256File(zipPath)
	os.WriteFile(zipPath+".sha256", []byte(hex+"  "+zipName+"\n"), 0o644)
	idx.Skills[0].Versions = append(idx.Skills[0].Versions, skills.SkillVersion{
		Version: "2.0.0", Source: "community", Channel: "stable",
		Zip: skills.Zip{Filename: zipName, SizeBytes: int64(buf.Len()), SHA256: "sha256:" + hex},
	})
	s := NewState(repo.NewDir(dist), nil, idx)

	// Delete only 1.0.0.
	rr := doDELETE(t, HandleSkillDelete(s), "/api/v1/skills/community/hello/1.0.0",
		map[string]string{"source": "community", "name": "hello", "version": "1.0.0"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	pub := s.SkillsIndex()
	if len(pub.Skills) != 1 || len(pub.Skills[0].Versions) != 1 {
		t.Fatalf("expected 1 skill with 1 version, got %+v", pub.Skills)
	}
	if pub.Skills[0].Versions[0].Version != "2.0.0" {
		t.Errorf("remaining version = %q, want 2.0.0", pub.Skills[0].Versions[0].Version)
	}
}

func TestHandleSkillDelete_AllVersionsUnderSource(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0",
		"---\nname: hello\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	// Add a second version under a different source — should survive.
	zipName := "hello-internal-1.0.0.zip"
	zipPath := filepath.Join(dist, "skills", zipName)
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	fh := &zip.FileHeader{Name: "SKILL.md", Method: zip.Deflate}
	fh.SetMode(0o644)
	w, _ := zw.CreateHeader(fh)
	w.Write([]byte("---\nname: hello\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n"))
	zw.Close()
	os.WriteFile(zipPath, buf.Bytes(), 0o644)
	hex, _ := skills.Sha256File(zipPath)
	os.WriteFile(zipPath+".sha256", []byte(hex+"  "+zipName+"\n"), 0o644)
	idx.Skills[0].Versions = append(idx.Skills[0].Versions, skills.SkillVersion{
		Version: "1.0.0", Source: "internal", Channel: "stable",
		Zip: skills.Zip{Filename: zipName, SizeBytes: int64(buf.Len()), SHA256: "sha256:" + hex},
	})
	s := NewState(repo.NewDir(dist), nil, idx)

	// Delete every version under source=community.
	rr := doDELETE(t, HandleSkillDelete(s), "/api/v1/skills/community/hello",
		map[string]string{"source": "community", "name": "hello"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp map[string][]string
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp["deleted"]) != 1 || resp["deleted"][0] != "hello-community-1.0.0.zip" {
		t.Errorf("deleted = %v", resp["deleted"])
	}
	// internal/1.0.0 should survive.
	pub := s.SkillsIndex()
	if len(pub.Skills) != 1 || len(pub.Skills[0].Versions) != 1 {
		t.Fatalf("expected 1 skill with 1 internal version, got %+v", pub.Skills)
	}
	if pub.Skills[0].Versions[0].Source != "internal" {
		t.Errorf("remaining source = %q, want internal", pub.Skills[0].Versions[0].Source)
	}
}

func TestHandleSkillDelete_NotFound(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0",
		"---\nname: hello\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	s := NewState(repo.NewDir(dist), nil, idx)

	rr := doDELETE(t, HandleSkillDelete(s), "/api/v1/skills/community/missing/1.0.0",
		map[string]string{"source": "community", "name": "missing", "version": "1.0.0"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestHandleSkillDelete_InvalidSource(t *testing.T) {
	dist, idx := skillFixture(t, "hello", "community", "1.0.0",
		"---\nname: hello\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	s := NewState(repo.NewDir(dist), nil, idx)

	rr := doDELETE(t, HandleSkillDelete(s), "/api/v1/skills/evil/hello",
		map[string]string{"source": "evil", "name": "hello"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}
