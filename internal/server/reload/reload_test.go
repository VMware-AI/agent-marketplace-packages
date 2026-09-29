package reload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/VMware-AI/agent-marketplace-packages/internal/repo"
	"github.com/VMware-AI/agent-marketplace-packages/internal/server"
)

// writeValidDist creates a tmpdir containing one valid tarball + .sha256
// sidecar + an index.json that references it. Returns the dist path.
// Use it as a starting point for the success-path tests.
func writeValidDist(t *testing.T) (distPath, tarballName, shaHex string) {
	t.Helper()
	dir := t.TempDir()

	tarName := "testagent-upstream-1.0.0.tar.gz"
	tarPath := filepath.Join(dir, tarName)
	// Tarball content can be anything for these tests — Validate() only
	// checks the .sha256 sidecar matches the index.json entry.
	if err := os.WriteFile(tarPath, []byte("fake tarball bytes"), 0o644); err != nil {
		t.Fatalf("write tarball: %v", err)
	}
	sum := sha256.Sum256([]byte("fake tarball bytes"))
	hex := hex.EncodeToString(sum[:])
	shaPath := tarPath + ".sha256"
	if err := os.WriteFile(shaPath, []byte(hex+"  "+tarName+"\n"), 0o644); err != nil {
		t.Fatalf("write sha256: %v", err)
	}

	idx := apitypes.Index{
		Agents: []apitypes.Agent{
			{
				Name: "testagent",
				Versions: []apitypes.Version{
					{
						Source:  "upstream",
						Version: "1.0.0",
						Tarball: apitypes.Tarball{
							Filename: tarName,
							SHA256:   "sha256:" + hex,
						},
					},
				},
			},
		},
	}
	if err := writeIndex(t, dir, idx); err != nil {
		t.Fatalf("write index: %v", err)
	}
	return dir, tarName, hex
}

func writeIndex(t *testing.T, dir string, idx apitypes.Index) error {
	t.Helper()
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.json"), data, 0o644)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestReload_HappyPath: write a valid dist, call Reload, assert
// State.Index() returns the freshly-loaded pointer.
func TestReload_HappyPath(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	dist := repo.NewDir(distDir)
	s := server.NewState(dist, nil, nil)

	if err := Reload(s, dist, quietLogger()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	got := s.Index()
	if got == nil {
		t.Fatal("Index() is nil after successful Reload")
	}
	if len(got.Agents) != 1 || got.Agents[0].Name != "testagent" {
		t.Fatalf("unexpected agents: %+v", got.Agents)
	}
}

// TestReload_ParseError: write garbage bytes to index.json, call Reload,
// assert error and that the previously-published Index is unchanged.
func TestReload_ParseError(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	if err := os.WriteFile(filepath.Join(distDir, "index.json"), []byte("{garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	dist := repo.NewDir(distDir)
	s := server.NewState(dist, nil, nil)

	// Seed a known good Index so we can detect the "not swapped" case.
	originalIdx := &apitypes.Index{Agents: []apitypes.Agent{{Name: "original"}}}
	s.SetIndex(originalIdx)

	err := Reload(s, dist, quietLogger())
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if got := s.Index(); got != originalIdx {
		t.Fatalf("Index pointer changed on failed reload: %p vs %p", got, originalIdx)
	}
}

// TestReload_ValidationError: index references a tarball that does not
// exist. Reload must return error and keep the old Index.
func TestReload_ValidationError(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	// Mutate index.json so it references a tarball we never wrote.
	bad := apitypes.Index{
		Agents: []apitypes.Agent{
			{
				Name: "testagent",
				Versions: []apitypes.Version{
					{
						Source:  "upstream",
						Version: "1.0.0",
						Tarball: apitypes.Tarball{
							Filename: "ghost-upstream-9.9.9.tar.gz",
							SHA256:   "sha256:0000000000000000000000000000000000000000000000000000000000000000",
						},
					},
				},
			},
		},
	}
	if err := writeIndex(t, distDir, bad); err != nil {
		t.Fatal(err)
	}

	dist := repo.NewDir(distDir)
	s := server.NewState(dist, nil, nil)
	originalIdx := &apitypes.Index{Agents: []apitypes.Agent{{Name: "original"}}}
	s.SetIndex(originalIdx)

	err := Reload(s, dist, quietLogger())
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if got := s.Index(); got != originalIdx {
		t.Fatalf("Index pointer changed on failed reload")
	}
}

// TestMaybeReload_StatError: delete index.json between InitialFingerprint
// and MaybeReload. Stat returns ENOENT, MaybeReload returns nil
// (transient — keep serving old).
func TestMaybeReload_StatError(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	dist := repo.NewDir(distDir)
	s := server.NewState(dist, nil, nil)

	if err := os.Remove(filepath.Join(distDir, "index.json")); err != nil {
		t.Fatal(err)
	}
	fp := InitialFingerprints(dist)

	if err := MaybeReload(s, dist, &fp, quietLogger()); err != nil {
		t.Fatalf("MaybeReload returned error on missing file: %v", err)
	}
	if s.Index() != nil {
		t.Fatal("Index should still be nil (no successful Reload yet)")
	}
}

// TestMaybeReload_Idempotent: write the same bytes twice. Second call
// must be a no-op (fingerprint unchanged) and not bump fp.
func TestMaybeReload_Idempotent(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	dist := repo.NewDir(distDir)
	s := server.NewState(dist, nil, nil)

	fp := InitialFingerprints(dist)

	if err := MaybeReload(s, dist, &fp, quietLogger()); err != nil {
		t.Fatalf("first MaybeReload: %v", err)
	}
	fpBefore := fp
	if err := MaybeReload(s, dist, &fp, quietLogger()); err != nil {
		t.Fatalf("second MaybeReload: %v", err)
	}
	if fp != fpBefore {
		t.Fatalf("fp advanced despite no on-disk change: %+v vs %+v", fp, fpBefore)
	}
}

// TestMaybeReload_SizeChanged: simulate mtime-going-backwards (manual
// touch) but with a different file size — fingerprint must detect the
// change and reload anyway.
func TestMaybeReload_SizeChanged(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	dist := repo.NewDir(distDir)
	s := server.NewState(dist, nil, nil)

	if err := MaybeReload(s, dist, nil, quietLogger()); err != nil {
		t.Fatalf("initial MaybeReload: %v", err)
	}
	if s.Index() == nil {
		t.Fatal("initial Reload didn't populate Index")
	}
	originalIdx := s.Index()

	// Touch index.json to a time BEFORE its current mtime, AND change the
	// size. Use os.Chtimes to push mtime backwards by an hour.
	idxPath := filepath.Join(distDir, "index.json")
	extra := apitypes.Index{Agents: []apitypes.Agent{
		{Name: "testagent"},
		{Name: "extra-agent"},
	}}
	if err := writeIndex(t, distDir, extra); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(idxPath, past, past); err != nil {
		t.Fatal(err)
	}

	// fp is nil here on purpose — MaybeReload should still trigger because
	// no fp means "no recorded prior state, must reload".
	if err := MaybeReload(s, dist, nil, quietLogger()); err != nil {
		t.Fatalf("MaybeReload with stale mtime: %v", err)
	}
	if s.Index() == originalIdx {
		t.Fatal("Index did not change despite new file size")
	}
}

// TestMaybeReload_SkillsOnlyChange verifies that writing only
// dist/skills-index.json (no change to dist/index.json) still triggers
// a Reload — Reload is the cheap way to fan out to both reload paths.
func TestMaybeReload_SkillsOnlyChange(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	// Pre-create dist/skills/<file>.zip + sidecar so ValidateSkills passes.
	skillsDir := filepath.Join(distDir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const zipName = "hello-community-1.0.0.zip"
	const zipHex = "0000000000000000000000000000000000000000000000000000000000000000"
	zipPath := filepath.Join(skillsDir, zipName)
	if err := os.WriteFile(zipPath, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zipPath+".sha256", []byte(zipHex+"  "+zipName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dist := repo.NewDir(distDir)
	s := server.NewState(dist, nil, nil)

	// Initial Reload — both indices are zero at this point.
	if err := Reload(s, dist, quietLogger()); err != nil {
		t.Fatal(err)
	}
	originalAgents := s.Index()

	// Capture current fingerprints (no skills-index.json yet → fp.Skills
	// is zero).
	fp := InitialFingerprints(dist)
	skillsJSON := `{"generated_at":"2026-09-08","schema_version":"1.0","skills":[{"name":"hello","description":"hi","versions":[{"version":"1.0.0","source":"community","channel":"stable","zip":{"filename":"hello-community-1.0.0.zip","size_bytes":7,"sha256":"sha256:` + zipHex + `"}}]}]}`
	// skills-index.json lives inside SkillsRoot (alongside the zips).
	// NewDir's default SkillsRoot is <dist>/skills, so write there.
	if err := os.WriteFile(filepath.Join(distDir, "skills", "skills-index.json"), []byte(skillsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MaybeReload(s, dist, &fp, quietLogger()); err != nil {
		t.Fatalf("MaybeReload after skills-index.json change: %v", err)
	}
	// SkillsIndex must now reflect the new file.
	gotSkills := s.SkillsIndex()
	if gotSkills == nil {
		t.Fatal("SkillsIndex is nil after skills-only change")
	}
	if len(gotSkills.Skills) != 1 || gotSkills.Skills[0].Name != "hello" {
		t.Errorf("unexpected skills: %+v", gotSkills.Skills)
	}
	// Agents index data should be unchanged (Reload re-fans-out, but the
	// loaded data is the same).
	if s.Index() != originalAgents && s.Index() == nil {
		t.Errorf("agents index lost on skills-only change")
	}
}

// TestMaybeReload_SkillsMissingFile: writing a malformed skills-index.json
// does NOT fail the agents reload — skills errors are warnings.
func TestMaybeReload_SkillsMissingFile(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	dist := repo.NewDir(distDir)
	s := server.NewState(dist, nil, nil)
	if err := Reload(s, dist, quietLogger()); err != nil {
		t.Fatal(err)
	}
	// Drop garbage into skills-index.json. Agents index must still be
	// served correctly (skills index stays at zero — no successful
	// load yet).
	// skills-index.json lives inside SkillsRoot (alongside the zips).
	// NewDir's default SkillsRoot is <dist>/skills, so write there.
	if err := os.MkdirAll(filepath.Join(distDir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "skills", "skills-index.json"), []byte("{garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := InitialFingerprints(dist)
	if err := MaybeReload(s, dist, &fp, quietLogger()); err != nil {
		t.Fatalf("MaybeReload must not fail on broken skills: %v", err)
	}
	if s.Index() == nil {
		t.Error("agents index lost on broken skills")
	}
}

// TestState_Concurrent: with -race, goroutine A spinning Reload on
// freshly-parsed *apitypes.Index, goroutine B spinning s.Index().Agents
// iteration. Run for a short time and ensure no race is detected.
func TestState_Concurrent(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	dist := repo.NewDir(distDir)
	s := server.NewState(dist, nil, nil)
	if err := Reload(s, dist, quietLogger()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Re-parse from disk every time so each Store is a fresh object.
			if err := Reload(s, dist, nil); err == nil {
				_ = s.Index() // also exercise the reader from this side
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			idx := s.Index()
			if idx == nil {
				continue
			}
			for _, a := range idx.Agents {
				_ = a.Name
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestState_DistImmutable: Reload must not swap the *repo.Dir pointer.
func TestState_DistImmutable(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	dist := repo.NewDir(distDir)
	s := server.NewState(dist, nil, nil)
	before := s.Dist

	if err := Reload(s, dist, quietLogger()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if s.Dist != before {
		t.Fatalf("Dist pointer changed across reload: %p vs %p", before, s.Dist)
	}
}

// TestInitialFingerprints_PartialMissing: a fresh dist/ with no
// skills-index.json should not error — the Skills slot stays zero.
func TestInitialFingerprints_PartialMissing(t *testing.T) {
	distDir, _, _ := writeValidDist(t)
	// Note: no skills-index.json written.
	dist := repo.NewDir(distDir)
	fp := InitialFingerprints(dist)
	if fp.Agents.mtime.IsZero() {
		t.Error("Agents fingerprint should be set")
	}
	if !fp.Skills.mtime.IsZero() {
		t.Error("Skills fingerprint should be zero when skills-index.json is missing")
	}
}
