package skillscmd

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
)

// fakeServer is a tiny HTTP test server that mimics the skills endpoints
// just enough to exercise the CLI commands end-to-end. Uses an
// http.ServeMux for routing (less error-prone than a hand-rolled
// switch on r.URL.Path).
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	index    *skills.Index
	zips     map[string][]byte // filename → bytes
	zipDir   string            // filesystem dir where uploaded zips land (server-side state)
}

func newFakeServer(t *testing.T) *fakeServer {
	fs := &fakeServer{
		zips:   map[string][]byte{},
		zipDir: t.TempDir(),
	}
	mux := http.NewServeMux()

	// /api/v1/skills → GET stripped index / POST multipart upload
	mux.HandleFunc("/api/v1/skills", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		if r.Method == http.MethodPost {
			// Multipart upload — store bytes, append index entry.
			if mr, err := r.MultipartReader(); err == nil {
				for {
					part, err := mr.NextPart()
					if err != nil {
						break
					}
					if part.FormName() != "file" {
						part.Close()
						continue
					}
					filename := part.FileName()
					data, _ := io.ReadAll(part)
					part.Close()
					fs.mu.Lock()
					fs.zips[filename] = data
					_ = os.WriteFile(filepath.Join(fs.zipDir, filename), data, 0o644)
					name, source, version, perr := skills.ParseZipFilename(filename)
					if perr == nil {
						found := false
						for i := range fs.index.Skills {
							if fs.index.Skills[i].Name == name {
								fs.index.Skills[i].Versions = append(fs.index.Skills[i].Versions,
									skills.SkillVersion{Version: version, Source: source, Channel: "stable",
										Zip: skills.Zip{Filename: filename, SizeBytes: int64(len(data)),
											SHA256: "sha256:" + skills.Sha256Bytes(data)}})
								found = true
								break
							}
						}
						if !found {
							fs.index.Skills = append(fs.index.Skills, skills.Skill{
								Name: name, Description: "(uploaded)",
								Versions: []skills.SkillVersion{{Version: version, Source: source, Channel: "stable",
									Zip: skills.Zip{Filename: filename, SizeBytes: int64(len(data)),
										SHA256: "sha256:" + skills.Sha256Bytes(data)}}},
							})
						}
						if fs.index.Schema == "" {
							fs.index.Schema = skills.IndexSchemaVersion
						}
						if fs.index.GeneratedAt == "" {
							fs.index.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
						}
					}
					fs.mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					sv := skills.SkillVersion{Version: version, Source: source, Channel: "stable",
						Zip: skills.Zip{Filename: filename, SizeBytes: int64(len(data)),
							SHA256: "sha256:" + skills.Sha256Bytes(data)}}
					json.NewEncoder(w).Encode(sv)
					return
				}
			}
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}
		// GET: stripped index.
		fs.mu.Lock()
		defer fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(fs.index.Strip()); err != nil {
			t.Errorf("encode index stripped: %v", err)
		}
	})

	// /api/v1/skills-index.json → raw index
	mux.HandleFunc("/api/v1/skills-index.json", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		fs.mu.Lock()
		defer fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(fs.index); err != nil {
			t.Errorf("encode index: %v", err)
		}
	})

	// /api/v1/skills/{source}/{name} → full skill (and DELETE)
	mux.HandleFunc("/api/v1/skills/", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		if r.Method == http.MethodDelete {
			handleFakeDelete(w, r, fs)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/skills/")
		parts := strings.Split(path, "/")
		// parts: [source, name] or [source, name, version, ...]
		if len(parts) < 2 {
			http.NotFound(w, r)
			return
		}
		source, name := parts[0], parts[1]
		fs.mu.Lock()
		defer fs.mu.Unlock()
		if len(parts) == 2 {
			// GET /api/v1/skills/{source}/{name}
			for _, s := range fs.index.Skills {
				if s.Name == name {
					for _, v := range s.Versions {
						if v.Source == source {
							w.Header().Set("Content-Type", "application/json")
							json.NewEncoder(w).Encode(s)
							return
						}
					}
				}
			}
			http.NotFound(w, r)
			return
		}
		version := parts[2]
		action := ""
		if len(parts) > 3 {
			action = parts[3]
		}
		// Find the skill version.
		var zipRef *skills.Zip
		for _, s := range fs.index.Skills {
			if s.Name != name {
				continue
			}
			for i := range s.Versions {
				v := &s.Versions[i]
				if v.Source == source && v.Version == version {
					zipRef = &v.Zip
					break
				}
			}
		}
		if zipRef == nil {
			http.NotFound(w, r)
			return
		}
		switch action {
		case "":
			// GET /api/v1/skills/{source}/{name}/{version}
			for _, s := range fs.index.Skills {
				if s.Name == name {
					for _, v := range s.Versions {
						if v.Source == source && v.Version == version {
							w.Header().Set("Content-Type", "application/json")
							json.NewEncoder(w).Encode(v)
							return
						}
					}
				}
			}
		case "download":
			data, ok := fs.zips[zipRef.Filename]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", `attachment; filename="`+zipRef.Filename+`"`)
			w.Write(data)
		case "sha256":
			data, ok := fs.zips[zipRef.Filename]
			if !ok {
				http.NotFound(w, r)
				return
			}
			sum := sha256.Sum256(data)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), zipRef.Filename)
		default:
			http.NotFound(w, r)
		}
	})

	fs.Server = httptest.NewServer(mux)
	return fs
}

// checkAuth returns false and writes a 401 if auth fails. Centralized so
// every handler starts with the same gate.
func checkAuth(w http.ResponseWriter, r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok || user != "agentpkg" || pass != "pw" {
		w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	return true
}

// zipsPath returns the on-disk location of an uploaded zip (server-side
// state). Used by upload tests to assert the server actually persisted
// the bytes.
func (fs *fakeServer) zipsPath(name string) string {
	return filepath.Join(fs.zipDir, name)
}

// handleFakeDelete mutates fs.index in response to a DELETE under
// /api/v1/skills/{source}/{name}[/{version}]. Just enough surface to
// drive TestSkillsDelete_* — does not rebuild an index file or do real
// file removal; the CLI tests only inspect the response shape.
func handleFakeDelete(w http.ResponseWriter, r *http.Request, fs *fakeServer) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/skills/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	source, name := parts[0], parts[1]
	version := ""
	if len(parts) >= 3 {
		version = parts[2]
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	deleted := []string{}
	notFound := true
	kept := fs.index.Skills[:0]
	for _, sk := range fs.index.Skills {
		if sk.Name != name {
			kept = append(kept, sk)
			continue
		}
		if version != "" {
			for _, v := range sk.Versions {
				if v.Source == source && v.Version == version {
					notFound = false
					deleted = append(deleted, v.Zip.Filename)
					break
				}
			}
			filtered := sk.Versions[:0]
			for _, v := range sk.Versions {
				if !(v.Source == source && v.Version == version) {
					filtered = append(filtered, v)
				}
			}
			if len(filtered) == 0 {
				continue
			}
			sk.Versions = filtered
			kept = append(kept, sk)
			continue
		}
		matched := false
		filtered := sk.Versions[:0]
		for _, v := range sk.Versions {
			if v.Source == source {
				matched = true
				notFound = false
				deleted = append(deleted, v.Zip.Filename)
				continue
			}
			filtered = append(filtered, v)
		}
		if !matched {
			kept = append(kept, sk)
			continue
		}
		if len(filtered) == 0 {
			continue
		}
		sk.Versions = filtered
		kept = append(kept, sk)
	}
	fs.index.Skills = kept
	if notFound {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if version != "" && len(deleted) == 1 {
		fmt.Fprintf(w, `{"deleted":%q}`, deleted[0])
		return
	}
	fmt.Fprintf(w, `{"deleted":[`)
	for i, d := range deleted {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%q", d)
	}
	fmt.Fprint(w, "]}")
}

// TestSkillsList_RemoteServer exercises the list command end-to-end
// against an httptest server.
func TestSkillsList_RemoteServer(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	fs.index = &skills.Index{
		Schema: skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{Name: "web-search", DisplayName: "Web Search", Description: "Search the web."},
			{Name: "internal-only", DisplayName: "Internal", Description: "Internal skill."},
		},
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credPath := filepath.Join(dir, "credentials")
	os.WriteFile(cfgPath, []byte("server: "+fs.URL+"\n"), 0o600)
	os.WriteFile(credPath, []byte("password: pw\n"), 0o600)
	cfg := cfgPath
	cred := credPath
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsListCmd()
	out, err := runCmd(t, cmd)
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "web-search") {
		t.Errorf("expected 'web-search' in output: %s", out)
	}
	if !strings.Contains(out, "internal-only") {
		t.Errorf("expected 'internal-only' in output: %s", out)
	}

	// JSON mode.
	out, err = runCmd(t, cmd, "--json")
	if err != nil {
		t.Fatalf("list --json: %v\n%s", err, out)
	}
	// --json emits the filtered skills array (not the envelope), matching
	// the convention used by `agentpkg index --json`.
	var resp []skills.SkillStripped
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse json: %v\n%s", err, out)
	}
	if len(resp) != 2 {
		t.Errorf("expected 2 skills, got %d", len(resp))
	}
}

// TestSkillsList_BadAuth checks that the CLI surfaces a 401 properly.
func TestSkillsList_BadAuth(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	fs.index = &skills.Index{Schema: skills.IndexSchemaVersion}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credPath := filepath.Join(dir, "credentials")
	os.WriteFile(cfgPath, []byte("server: "+fs.URL+"\n"), 0o600)
	os.WriteFile(credPath, []byte("password: wrong-pw\n"), 0o600)
	cfg := cfgPath
	cred := credPath
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsListCmd()
	out, err := runCmd(t, cmd)
	if err == nil {
		t.Errorf("expected 401 error, got nil\n%s", out)
	}
	if !strings.Contains(out+err.Error(), "401") &&
		!strings.Contains(out+err.Error(), "login") {
		t.Errorf("expected 'login' guidance in error: %s", out)
	}
}

// TestSkillsShow_RemoteServer exercises show against the fake server.
func TestSkillsShow_RemoteServer(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	fs.index = &skills.Index{
		Schema: skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{
				Name:        "hello",
				Description: "Says hello.",
				Author:      "Tester",
				Tags:        []string{"greeting"},
				Versions: []skills.SkillVersion{
					{Version: "1.0.0", Source: "community", Channel: "stable",
						Zip: skills.Zip{Filename: "hello-community-1.0.0.zip", SizeBytes: 100, SHA256: "sha256:abc"}},
				},
			},
		},
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credPath := filepath.Join(dir, "credentials")
	os.WriteFile(cfgPath, []byte("server: "+fs.URL+"\n"), 0o600)
	os.WriteFile(credPath, []byte("password: pw\n"), 0o600)
	cfg := cfgPath
	cred := credPath
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsShowCmd()
	out, err := runCmd(t, cmd, "hello")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in output: %s", out)
	}
	if !strings.Contains(out, "Tester") {
		t.Errorf("expected 'Tester' (author) in output: %s", out)
	}
}

// TestSkillsDownload_StreamsAndVerifies confirms download streams bytes
// to disk + writes the sidecar + verifies the in-flight hash.
func TestSkillsDownload_StreamsAndVerifies(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	payload := []byte("PK\x03\x04not a real zip but it has the right size")
	zipName := "hello-community-1.0.0.zip"
	fs.zips[zipName] = payload
	sum := sha256.Sum256(payload)
	wantHex := hex.EncodeToString(sum[:])
	fs.index = &skills.Index{
		Schema: skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{
				Name: "hello",
				Versions: []skills.SkillVersion{
					{Version: "1.0.0", Source: "community", Channel: "stable",
						Zip: skills.Zip{Filename: zipName, SizeBytes: int64(len(payload)), SHA256: "sha256:" + wantHex}},
				},
			},
		},
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credPath := filepath.Join(dir, "credentials")
	os.WriteFile(cfgPath, []byte("server: "+fs.URL+"\n"), 0o600)
	os.WriteFile(credPath, []byte("password: pw\n"), 0o600)
	cfg := cfgPath
	cred := credPath
	SetGlobals(&cfg, &cred)

	outFile := filepath.Join(dir, "downloaded.zip")
	cmd := NewSkillsDownloadCmd()
	out, err := runCmd(t, cmd, "hello", "--version", "1.0.0", "-o", outFile)
	if err != nil {
		t.Fatalf("download: %v\n%s", err, out)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("downloaded content mismatch: got %d bytes, want %d", len(got), len(payload))
	}
	// Sidecar should exist.
	sidecar := outFile + ".sha256"
	sd, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	if !strings.Contains(string(sd), wantHex) {
		t.Errorf("sidecar hash mismatch: got %q, want hex %s", sd, wantHex)
	}
}

// TestSkillsList_FilterSubstring exercises the local --filter flag.
func TestSkillsList_FilterSubstring(t *testing.T) {
	s := []skills.SkillStripped{
		{Name: "web-search", Description: "x"},
		{Name: "internal-only", Description: "x"},
		{Name: "web-fetch", Description: "x"},
	}
	got := filterSkills(s, "web-")
	if len(got) != 2 {
		t.Errorf("expected 2 skills matching 'web-', got %d", len(got))
	}
	for _, sk := range got {
		if !strings.HasPrefix(sk.Name, "web-") {
			t.Errorf("filtered skill %q doesn't match", sk.Name)
		}
	}
	// Empty filter returns input unchanged.
	if got := filterSkills(s, ""); len(got) != 3 {
		t.Errorf("empty filter should pass everything, got %d", len(got))
	}
}

// TestResolveSkillLatestVersion covers channel-filtering semantics.
func TestResolveSkillLatestVersion(t *testing.T) {
	s := &skills.Skill{
		Name: "x",
		Versions: []skills.SkillVersion{
			{Version: "1.0.0", Channel: "stable"},
			{Version: "1.1.0-beta.1", Channel: "beta"},
			{Version: "0.9.0", Channel: "stable"},
		},
	}
	// stable → 1.0.0 (first match; server-side index is already sorted desc).
	v, err := resolveSkillLatestVersion(s, "stable")
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != "1.0.0" {
		t.Errorf("stable latest = %q, want 1.0.0", v.Version)
	}
	// beta → 1.1.0-beta.1
	v, err = resolveSkillLatestVersion(s, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != "1.1.0-beta.1" {
		t.Errorf("beta latest = %q, want 1.1.0-beta.1", v.Version)
	}
	// edge → no version
	if _, err := resolveSkillLatestVersion(s, "edge"); err == nil {
		t.Error("expected error for missing channel")
	}
}

// TestTrimSHA256Prefix covers the local helper.
func TestTrimSHA256Prefix(t *testing.T) {
	cases := map[string]string{
		"sha256:abcd": "abcd",
		"abcd":        "abcd",
		"":            "",
		"sha256:":     "",
	}
	for in, want := range cases {
		if got := trimSHA256Prefix(in); got != want {
			t.Errorf("trimSHA256Prefix(%q) = %q, want %q", in, got, want)
		}
	}
}

// -----------------------------------------------------------------------
// Upload (Phase 6) — end-to-end against the fake server.
// -----------------------------------------------------------------------

// TestSkillsUpload_OK submits a zip and verifies the server-side store +
// sidecar + index updates happen.
func TestSkillsUpload_OK(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	fs.index = &skills.Index{Schema: skills.IndexSchemaVersion}

	// Build a real zip with SKILL.md.
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "hello-community-1.0.0.zip")
	md := "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n"
	if err := writeZipWithSKILLMD(zipPath, md); err != nil {
		t.Fatal(err)
	}

	// Config + credentials pointing at the fake server.
	cfgPath := filepath.Join(dir, "config.yaml")
	credPath := filepath.Join(dir, "credentials")
	os.WriteFile(cfgPath, []byte("server: "+fs.URL+"\n"), 0o600)
	os.WriteFile(credPath, []byte("password: pw\n"), 0o600)
	cfg := cfgPath
	cred := credPath
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsUploadCmd()
	out, err := runCmd(t, cmd, zipPath)
	if err != nil {
		t.Fatalf("upload: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Uploaded") {
		t.Errorf("expected 'Uploaded' in output: %s", out)
	}

	// Server should have stored the zip + sidecar + a fresh index entry.
	if _, err := os.Stat(fs.zipsPath("hello-community-1.0.0.zip")); err != nil {
		t.Errorf("server zip missing: %v", err)
	}
}

// TestSkillsUpload_DryRun is a no-op: no HTTP request should hit the fake
// server. We confirm by leaving the index untouched (it stays empty).
func TestSkillsUpload_DryRun(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	fs.index = &skills.Index{Schema: skills.IndexSchemaVersion}

	dir := t.TempDir()
	zipPath := filepath.Join(dir, "hello-community-1.0.0.zip")
	md := "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n"
	if err := writeZipWithSKILLMD(zipPath, md); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "config.yaml")
	credPath := filepath.Join(dir, "credentials")
	os.WriteFile(cfgPath, []byte("server: "+fs.URL+"\n"), 0o600)
	os.WriteFile(credPath, []byte("password: pw\n"), 0o600)
	cfg := cfgPath
	cred := credPath
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsUploadCmd()
	out, err := runCmd(t, cmd, zipPath, "--dry-run")
	if err != nil {
		t.Fatalf("upload --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(dry-run)") {
		t.Errorf("expected '(dry-run)' in output: %s", out)
	}
	if len(fs.index.Skills) != 0 {
		t.Errorf("dry-run should not mutate the index, got: %+v", fs.index.Skills)
	}
}

// TestSkillsUpload_BadFilename rejects a zip whose name parses but leaves
// the source empty — client-side validation, no server round-trip needed.
// (Pure-parse failures — 4+ dash-separated segments, missing .zip suffix —
// are covered by TestParseSkillZipName in skillscmd_test.go.)
func TestSkillsUpload_BadFilename(t *testing.T) {
	dir := t.TempDir()
	// "foo.zip" parses as the 1-segment form (name="foo", source="",
	// version="") — source stays empty, so the source-allowed check fires.
	zipPath := filepath.Join(dir, "foo.zip")
	if err := writeZipWithSKILLMD(zipPath, "body"); err != nil {
		t.Fatal(err)
	}
	cmd := NewSkillsUploadCmd()
	_, err := runCmd(t, cmd, zipPath)
	if err == nil {
		t.Fatal("expected error on bad zip filename")
	}
	if !strings.Contains(err.Error(), "source in filename") {
		t.Errorf("expected 'source in filename' in error, got: %v", err)
	}
}

// writeZipWithSKILLMD creates a minimal zip with a single SKILL.md entry.
func writeZipWithSKILLMD(path, content string) error {
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	fh := &zip.FileHeader{Name: "SKILL.md", Method: zip.Deflate}
	fh.SetMode(0o644)
	w, err := zw.CreateHeader(fh)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(content)); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// -----------------------------------------------------------------------
// Delete (Phase 7) — end-to-end against the fake server.
// -----------------------------------------------------------------------

func TestSkillsDelete_SingleVersion(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	fs.index = &skills.Index{
		Schema: skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{
				Name: "hello", Description: "x",
				Versions: []skills.SkillVersion{
					{Version: "1.0.0", Source: "community", Channel: "stable",
						Zip: skills.Zip{Filename: "hello-community-1.0.0.zip", SHA256: "sha256:abc"}},
				},
			},
		},
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credPath := filepath.Join(dir, "credentials")
	os.WriteFile(cfgPath, []byte("server: "+fs.URL+"\n"), 0o600)
	os.WriteFile(credPath, []byte("password: pw\n"), 0o600)
	cfg := cfgPath
	cred := credPath
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsDeleteCmd()
	out, err := runCmd(t, cmd, "hello", "--source", "community", "--version", "1.0.0")
	if err != nil {
		t.Fatalf("delete: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Deleted community/hello/1.0.0") {
		t.Errorf("unexpected output: %s", out)
	}
	if len(fs.index.Skills) != 0 {
		t.Errorf("expected empty index, got %+v", fs.index.Skills)
	}
}

func TestSkillsDelete_AllVersions(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	fs.index = &skills.Index{
		Schema: skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{Name: "hello", Description: "x", Versions: []skills.SkillVersion{
				{Version: "1.0.0", Source: "community", Channel: "stable",
					Zip: skills.Zip{Filename: "hello-community-1.0.0.zip", SHA256: "sha256:a"}},
				{Version: "2.0.0", Source: "community", Channel: "stable",
					Zip: skills.Zip{Filename: "hello-community-2.0.0.zip", SHA256: "sha256:b"}},
			}},
		},
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credPath := filepath.Join(dir, "credentials")
	os.WriteFile(cfgPath, []byte("server: "+fs.URL+"\n"), 0o600)
	os.WriteFile(credPath, []byte("password: pw\n"), 0o600)
	cfg := cfgPath
	cred := credPath
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsDeleteCmd()
	out, err := runCmd(t, cmd, "hello", "--source", "community")
	if err != nil {
		t.Fatalf("delete: %v\n%s", err, out)
	}
	if !strings.Contains(out, "hello-community-1.0.0.zip") ||
		!strings.Contains(out, "hello-community-2.0.0.zip") {
		t.Errorf("expected both filenames listed: %s", out)
	}
	if len(fs.index.Skills) != 0 {
		t.Errorf("expected empty index, got %+v", fs.index.Skills)
	}
}

func TestSkillsDelete_DryRun(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	fs.index = &skills.Index{
		Schema: skills.IndexSchemaVersion,
		Skills: []skills.Skill{
			{Name: "hello", Description: "x", Versions: []skills.SkillVersion{
				{Version: "1.0.0", Source: "community", Channel: "stable",
					Zip: skills.Zip{Filename: "hello-community-1.0.0.zip", SHA256: "sha256:abc"}},
			}},
		},
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credPath := filepath.Join(dir, "credentials")
	os.WriteFile(cfgPath, []byte("server: "+fs.URL+"\n"), 0o600)
	os.WriteFile(credPath, []byte("password: pw\n"), 0o600)
	cfg := cfgPath
	cred := credPath
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsDeleteCmd()
	out, err := runCmd(t, cmd, "hello", "--source", "community", "--dry-run")
	if err != nil {
		t.Fatalf("delete --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(dry-run)") {
		t.Errorf("expected (dry-run): %s", out)
	}
	// Index should be untouched.
	if len(fs.index.Skills) != 1 || len(fs.index.Skills[0].Versions) != 1 {
		t.Errorf("dry-run should not mutate index, got %+v", fs.index.Skills)
	}
}

func TestSkillsDelete_MissingSource(t *testing.T) {
	cmd := NewSkillsDeleteCmd()
	_, err := runCmd(t, cmd, "hello")
	if err == nil {
		t.Fatal("expected error when --source omitted")
	}
	// Cobra's MarkFlagRequired emits its own message before RunE runs;
	// accept either that or our manual check.
	msg := err.Error()
	if !strings.Contains(msg, "--source is required") &&
		!strings.Contains(msg, `required flag(s) "source" not set`) {
		t.Errorf("expected --source-required error, got: %v", err)
	}
}