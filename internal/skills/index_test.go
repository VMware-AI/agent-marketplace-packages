package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// roundtripJSON is a small helper to verify that MarshalIndex then
// ParseIndexBytes reproduces the original. Used in several tests below.
func roundtripJSON(t *testing.T, idx *Index) *Index {
	t.Helper()
	data, err := MarshalIndex(idx)
	if err != nil {
		t.Fatalf("MarshalIndex: %v", err)
	}
	got, err := ParseIndexBytes(data)
	if err != nil {
		t.Fatalf("ParseIndexBytes: %v", err)
	}
	return got
}

func TestParseIndexBytes_Roundtrip(t *testing.T) {
	in := &Index{
		GeneratedAt: "2026-09-08T10:00:00Z",
		Schema:      IndexSchemaVersion,
		Skills: []Skill{
			{
				Name:        "web-search",
				DisplayName: "Web Search",
				Description: "Search the web.",
				Author:      "Jane",
				Tags:        []string{"search"},
				Versions: []SkillVersion{
					{
						Version:    "1.0.0",
						Source:     "community",
						Channel:    "stable",
						ReleasedAt: "2026-09-01",
						Zip:        Zip{Filename: "web-search-community-1.0.0.zip", SizeBytes: 1234, SHA256: "sha256:abc"},
						Body:       "# Body",
					},
				},
			},
		},
	}
	out := roundtripJSON(t, in)
	if len(out.Skills) != 1 || out.Skills[0].Name != "web-search" {
		t.Fatalf("roundtrip lost skill: %+v", out.Skills)
	}
	if len(out.Skills[0].Versions) != 1 {
		t.Fatalf("roundtrip lost versions")
	}
	if out.Skills[0].Versions[0].Zip.Filename != "web-search-community-1.0.0.zip" {
		t.Errorf("zip filename lost")
	}
	if out.GeneratedAt != in.GeneratedAt {
		t.Errorf("generated_at lost")
	}
}

func TestParseIndexBytes_Empty(t *testing.T) {
	in := []byte(`{"generated_at":"2026-09-08T10:00:00Z","schema_version":"1.0","skills":[]}`)
	idx, err := ParseIndexBytes(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Skills) != 0 {
		t.Errorf("expected empty Skills, got %d", len(idx.Skills))
	}
}

func TestParseIndexBytes_InvalidJSON(t *testing.T) {
	_, err := ParseIndexBytes([]byte(`{not valid json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseIndex_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skills-index.json")
	want := &Index{
		GeneratedAt: "2026-09-08T10:00:00Z",
		Schema:      IndexSchemaVersion,
		Skills: []Skill{
			{Name: "x", Description: "x skill", Versions: []SkillVersion{
				{Version: "1.0.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "x.zip", SHA256: "sha256:abc"}},
			}},
		},
	}
	data, err := MarshalIndex(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ParseIndex(path)
	if err != nil {
		t.Fatalf("ParseIndex: %v", err)
	}
	if got.Skills[0].Name != "x" {
		t.Errorf("round-trip via file lost data")
	}
}

func TestParseIndex_FileMissing(t *testing.T) {
	_, err := ParseIndex("/nonexistent/path/skills-index.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestStrip_DropsBodyAndInputs(t *testing.T) {
	in := &Index{
		GeneratedAt: "2026-09-08T10:00:00Z",
		Schema:      IndexSchemaVersion,
		Skills: []Skill{
			{
				Name: "x", Description: "x",
				Versions: []SkillVersion{
					{
						Version: "1.0.0", Source: "community", Channel: "stable",
						Zip:    Zip{Filename: "x.zip", SizeBytes: 100, SHA256: "sha256:abc"},
						Body:   "SHOULD_NOT_APPEAR",
						Inputs: []Input{{Name: "foo", Type: "string"}},
						Requires: &Requires{OS: []string{"linux"}},
						EntryPoint: "scripts/run.sh",
					},
				},
			},
		},
	}
	out := in.Strip()
	if out == nil {
		t.Fatal("Strip returned nil")
	}
	if len(out.Skills) != 1 || len(out.Skills[0].Versions) != 1 {
		t.Fatalf("Strip dropped skills: %+v", out)
	}
	v := out.Skills[0].Versions[0]
	if v.Zip.Filename != "x.zip" {
		t.Errorf("Zip.Filename lost in Strip")
	}
	// The stripped projection is the SkillVersionStripped struct, which has
	// no Body / Inputs / Requires / EntryPoint fields — so there's nothing
	// to assert here other than "the right fields are present".
	if v.Version != "1.0.0" {
		t.Errorf("Version lost in Strip")
	}
	if v.Channel != "stable" {
		t.Errorf("Channel lost in Strip")
	}
}

func TestFilterByChannel_PicksLatest(t *testing.T) {
	idx := &Index{
		GeneratedAt: "t", Schema: IndexSchemaVersion,
		Skills: []Skill{
			{
				Name: "x", Description: "x",
				Versions: []SkillVersion{
					{Version: "1.0.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "x-stable-1.0.0.zip", SHA256: "sha256:a"}},
					{Version: "1.1.0", Source: "community", Channel: "beta", Zip: Zip{Filename: "x-beta-1.1.0.zip", SHA256: "sha256:b"}},
					{Version: "1.1.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "x-stable-1.1.0.zip", SHA256: "sha256:c"}},
					{Version: "1.0.5", Source: "community", Channel: "stable", Zip: Zip{Filename: "x-stable-1.0.5.zip", SHA256: "sha256:d"}},
				},
			},
		},
	}
	out := idx.Strip().FilterByChannel("stable")
	if len(out.Skills) != 1 {
		t.Fatalf("expected 1 skill in stable, got %d", len(out.Skills))
	}
	v := out.Skills[0].Versions[0]
	if v.Version != "1.1.0" {
		t.Errorf("latest in stable = %q, want 1.1.0", v.Version)
	}
}

func TestFilterByChannel_LatestAlias(t *testing.T) {
	idx := &Index{Schema: IndexSchemaVersion,
		Skills: []Skill{
			{Name: "x", Description: "x", Versions: []SkillVersion{
				{Version: "1.0.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "x.zip", SHA256: "sha256:a"}},
			}},
		},
	}
	out := idx.Strip().FilterByChannel("latest")
	if len(out.Skills) != 1 || out.Skills[0].Versions[0].Version != "1.0.0" {
		t.Errorf("latest alias failed: %+v", out)
	}
}

func TestFilterByChannel_NoMatch(t *testing.T) {
	idx := &Index{Schema: IndexSchemaVersion,
		Skills: []Skill{
			{Name: "x", Description: "x", Versions: []SkillVersion{
				{Version: "1.0.0", Source: "community", Channel: "beta", Zip: Zip{Filename: "x.zip", SHA256: "sha256:a"}},
			}},
		},
	}
	out := idx.Strip().FilterByChannel("stable")
	if len(out.Skills) != 0 {
		t.Errorf("expected 0 skills (no stable version), got %d", len(out.Skills))
	}
}

func TestFilterByChannel_SemverNotString(t *testing.T) {
	// 1.10.0 > 1.9.0 in semver but "1.9.0" > "1.10.0" lexicographically.
	// Confirm we use semver, not string compare.
	idx := &Index{Schema: IndexSchemaVersion,
		Skills: []Skill{
			{Name: "x", Description: "x", Versions: []SkillVersion{
				{Version: "1.9.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "x-1.9.0.zip", SHA256: "sha256:a"}},
				{Version: "1.10.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "x-1.10.0.zip", SHA256: "sha256:b"}},
			}},
		},
	}
	out := idx.Strip().FilterByChannel("stable")
	if v := out.Skills[0].Versions[0]; v.Version != "1.10.0" {
		t.Errorf("latest = %q, want 1.10.0 (semver, not string)", v.Version)
	}
}

func TestSortVersions_DescendingSemver(t *testing.T) {
	s := &Skill{Name: "x", Description: "x", Versions: []SkillVersion{
		{Version: "0.1.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "a"}},
		{Version: "1.10.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "b"}},
		{Version: "1.2.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "c"}},
		{Version: "1.2.0-alpha", Source: "community", Channel: "stable", Zip: Zip{Filename: "d"}},
	}}
	s.SortVersions()
	want := []string{"1.10.0", "1.2.0", "1.2.0-alpha", "0.1.0"}
	for i, w := range want {
		if s.Versions[i].Version != w {
			t.Errorf("position %d = %q, want %q", i, s.Versions[i].Version, w)
		}
	}
}

func TestSortVersions_StableOnTies(t *testing.T) {
	// Two identical semver versions should keep their input order.
	s := &Skill{Name: "x", Description: "x", Versions: []SkillVersion{
		{Version: "1.0.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "first"}},
		{Version: "1.0.0", Source: "community", Channel: "stable", Zip: Zip{Filename: "second"}},
	}}
	s.SortVersions()
	if s.Versions[0].Zip.Filename != "first" || s.Versions[1].Zip.Filename != "second" {
		t.Errorf("stable sort on ties failed: %+v", s.Versions)
	}
}

func TestFindSkill(t *testing.T) {
	idx := &Index{
		Skills: []Skill{
			{Name: "a", Description: "a"},
			{Name: "b", Description: "b"},
		},
	}
	if s, ok := idx.FindSkill("a"); !ok || s.Name != "a" {
		t.Errorf("FindSkill(a) = %v, %v", s, ok)
	}
	if _, ok := idx.FindSkill("nope"); ok {
		t.Error("FindSkill(nope) returned ok=true")
	}
}

func TestFindVersion(t *testing.T) {
	s := &Skill{Name: "x", Description: "x", Versions: []SkillVersion{
		{Version: "1.0.0", Source: "community", Channel: "stable"},
		{Version: "2.0.0", Source: "internal", Channel: "stable"},
	}}
	if v, ok := s.FindVersion("community", "1.0.0"); !ok || v.Version != "1.0.0" {
		t.Errorf("FindVersion(community,1.0.0) = %v, %v", v, ok)
	}
	if _, ok := s.FindVersion("community", "2.0.0"); ok {
		t.Error("FindVersion(community,2.0.0) returned ok=true (cross-source)")
	}
	if _, ok := s.FindVersion("internal", "1.0.0"); ok {
		t.Error("FindVersion(internal,1.0.0) returned ok=true (cross-version)")
	}
}

func TestMarshalIndex_Indent(t *testing.T) {
	idx := &Index{
		GeneratedAt: "t", Schema: IndexSchemaVersion,
		Skills: []Skill{{Name: "x", Description: "x"}},
	}
	data, err := MarshalIndex(idx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n  ") {
		t.Error("MarshalIndex should produce indented JSON (2 spaces)")
	}
	// Sanity: parses back.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Errorf("MarshalIndex output does not parse as JSON: %v", err)
	}
}
