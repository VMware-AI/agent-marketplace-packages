package skillscmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
)

// mustSeedFakeSkill builds a zip from skillMD and registers it in the
// fake server's index so the CLI can resolve / install it via the
// registry. Used by batch install tests.
func mustSeedFakeSkill(t *testing.T, fs *fakeServer, name, version, skillMD string) {
	t.Helper()
	dir := t.TempDir()
	zipPath := filepath.Join(dir, name+"-"+version+".zip")
	if err := writeZipWithSKILLMD(zipPath, skillMD); err != nil {
		t.Fatalf("writeZipWithSKILLMD: %v", err)
	}
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	sum := sha256.Sum256(data)
	filename := name + "-community-" + version + ".zip"

	fs.mu.Lock()
	defer fs.mu.Unlock()
	// Persist zip on the server side so /download returns it.
	fs.zips[filename] = data
	if err := os.WriteFile(filepath.Join(fs.zipDir, filename), data, 0o644); err != nil {
		t.Fatalf("persist zip: %v", err)
	}
	// Persist sha256 sidecar.
	hexSum := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(fs.zipDir, filename+".sha256"),
		[]byte(hexSum+"  "+filename+"\n"), 0o644); err != nil {
		t.Fatalf("persist sha256: %v", err)
	}

	// Register in the index. The CLI's install command reads the
	// stripped /skills endpoint to resolve "latest in channel".
	idx := fs.index
	if idx == nil {
		idx = &skills.Index{Schema: skills.IndexSchemaVersion}
		fs.index = idx
	}
	var sk *skills.Skill
	for i := range idx.Skills {
		if idx.Skills[i].Name == name {
			sk = &idx.Skills[i]
			break
		}
	}
	if sk == nil {
		idx.Skills = append(idx.Skills, skills.Skill{
			Name:        name,
			Description: extractDescription(skillMD),
			Category:    "dev",
		})
		sk = &idx.Skills[len(idx.Skills)-1]
	}
	sk.Versions = append(sk.Versions, skills.SkillVersion{
		Version:       version,
		Source:        "community",
		Channel:       "stable",
		Agents:        []string{"all"},
		InstallMethod: "zip-extract",
		Zip: skills.Zip{
			Filename:  filename,
			SizeBytes: int64(len(data)),
			SHA256:    "sha256:" + hexSum,
		},
	})
}

// extractDescription pulls the description out of SKILL.md YAML
// frontmatter. Best-effort — used only for fake-server seeding where
// the description is informational.
func extractDescription(skillMD string) string {
	const sep = "\n---\n"
	open := sep
	i := strings.Index(skillMD, open)
	if i < 0 {
		return "seeded skill"
	}
	rest := skillMD[i+len(open):]
	j := strings.Index(rest, sep)
	if j < 0 {
		return "seeded skill"
	}
	for _, line := range strings.Split(rest[:j], "\n") {
		if strings.HasPrefix(line, "description:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	return "seeded skill"
}

// configPath writes an agentpkg config.yaml pointing at this fake server
// and returns its path. The file lives in t.TempDir() so the test
// harness cleans it up automatically.
func (fs *fakeServer) configPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server: "+fs.URL+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

// credentialsPath writes a credentials file with the fake server's
// password and returns its path.
func (fs *fakeServer) credentialsPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(p, []byte("password: pw\n"), 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	return p
}
