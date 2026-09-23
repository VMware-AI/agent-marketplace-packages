package skills

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildFixtureDir creates a minimal valid skill directory tree under a tmp
// directory and returns the path. The tree contains:
//   - SKILL.md (valid)
//   - scripts/run.sh (executable)
//   - references/notes.md (regular file)
//   - assets/logo.png (regular file, no executable bit)
func buildFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	skillMD := `---
name: test-skill
description: A test skill for verifying zip round-trip behavior in unit tests.
version: 1.0.0
author: Tester
category: dev
agents: [all]
tags: [test]
metadata:
  entry_point: scripts/run.sh
---

# Test Skill

Long body. Has multiple paragraphs and --- dashes that should not confuse
the frontmatter parser.

## Usage
...
`
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "run.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	refsDir := filepath.Join(dir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "notes.md"), []byte("# Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assetsDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "logo.png"), []byte("PNG_BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildFromDir_BuildsValidZip(t *testing.T) {
	dir := buildFixtureDir(t)
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	m, err := BuildFromDir(dir, zipPath)
	if err != nil {
		t.Fatalf("BuildFromDir: %v", err)
	}
	if m.Name != "test-skill" {
		t.Errorf("returned manifest name = %q", m.Name)
	}
	// File must exist.
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("output zip missing: %v", err)
	}
}

func TestBuildFromDir_PreservesExecutableBit(t *testing.T) {
	dir := buildFixtureDir(t)
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	if _, err := BuildFromDir(dir, zipPath); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var found bool
	for _, f := range zr.File {
		if f.Name == "scripts/run.sh" {
			found = true
			if f.Mode().Perm()&0o111 == 0 {
				t.Errorf("scripts/run.sh should be executable, got mode %v", f.Mode())
			}
		}
	}
	if !found {
		t.Error("scripts/run.sh missing from zip")
	}
}

func TestBuildFromDir_MissingSKILLMD(t *testing.T) {
	dir := t.TempDir() // empty — no SKILL.md
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	_, err := BuildFromDir(dir, zipPath)
	if err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("expected SKILL.md error, got %v", err)
	}
}

func TestExtractSkillMD_RoundTrip(t *testing.T) {
	dir := buildFixtureDir(t)
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	if _, err := BuildFromDir(dir, zipPath); err != nil {
		t.Fatal(err)
	}
	m, raw, err := ExtractSkillMD(zipPath)
	if err != nil {
		t.Fatalf("ExtractSkillMD: %v", err)
	}
	if m.Name != "test-skill" {
		t.Errorf("extracted name = %q", m.Name)
	}
	if !bytes.Contains(raw, []byte("Test Skill")) {
		t.Error("raw bytes should contain body marker")
	}
}

func TestExtractSkillMD_NoSKILLMD(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "no-skill.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	if _, err := zw.Create("README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Create("scripts/run.sh"); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	f.Close()
	_, _, err = ExtractSkillMD(zipPath)
	if err == nil || !strings.Contains(err.Error(), "no SKILL.md") {
		t.Fatalf("expected 'no SKILL.md' error, got %v", err)
	}
}

func TestExtractSkillMD_SKILLMDInSubdirRejected(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "nested.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	if _, err := zw.Create("subdir/SKILL.md"); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	f.Close()
	_, _, err = ExtractSkillMD(zipPath)
	if err == nil {
		t.Fatal("expected error for nested SKILL.md")
	}
}

func TestExtractSkillMD_MalformedFrontmatter(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "bad.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	// Valid frontmatter shape but missing required fields.
	w.Write([]byte("---\ndescription: no name\n---\nbody\n"))
	zw.Close()
	f.Close()
	_, _, err = ExtractSkillMD(zipPath)
	if err == nil {
		t.Fatal("expected validation error for missing name")
	}
}

func TestExtractAllEntries_RoundTrip(t *testing.T) {
	dir := buildFixtureDir(t)
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	if _, err := BuildFromDir(dir, zipPath); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	if err := ExtractAllEntries(zipPath, destDir); err != nil {
		t.Fatalf("ExtractAllEntries: %v", err)
	}
	// Verify all expected files exist with content.
	checks := []struct {
		path    string
		content string
		mode    os.FileMode
	}{
		{"SKILL.md", "test-skill", 0o644},
		{"scripts/run.sh", "echo ok", 0o755},
		{"references/notes.md", "Notes", 0o644},
		{"assets/logo.png", "PNG_BYTES", 0o644},
	}
	for _, c := range checks {
		full := filepath.Join(destDir, c.path)
		data, err := os.ReadFile(full)
		if err != nil {
			t.Errorf("missing extracted file %s: %v", c.path, err)
			continue
		}
		if !strings.Contains(string(data), c.content) {
			t.Errorf("%s content mismatch: want substring %q, got %q", c.path, c.content, string(data))
		}
		// scripts/* should retain executable bit.
		if c.path == "scripts/run.sh" {
			fi, err := os.Stat(full)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm()&0o111 == 0 {
				t.Errorf("%s should be executable, mode = %v", c.path, fi.Mode())
			}
		}
	}
}

func TestExtractAllEntries_RejectsZipSlip(t *testing.T) {
	// Build a zip with a malicious entry that escapes the destination.
	zipPath := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("evil"))
	zw.Close()
	f.Close()
	destDir := t.TempDir()
	err = ExtractAllEntries(zipPath, destDir)
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("expected 'unsafe path' error, got %v", err)
	}
}

func TestListZipNames(t *testing.T) {
	dir := buildFixtureDir(t)
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	if _, err := BuildFromDir(dir, zipPath); err != nil {
		t.Fatal(err)
	}
	names, err := ListZipNames(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"SKILL.md",
		"assets/",
		"assets/logo.png",
		"references/",
		"references/notes.md",
		"scripts/",
		"scripts/run.sh",
	}
	if len(names) != len(want) {
		t.Fatalf("names count = %d (%v), want %d", len(names), names, len(want))
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("names[%d] = %q, want %q", i, names[i], w)
		}
	}
}

func TestSha256File(t *testing.T) {
	// Hash of the literal "abc\n" — known constant.
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "edeaaff3f1774ad2888673770c6d64097e391bc362d7d6fb34982ddf0efd18cb"
	if got != want {
		t.Errorf("Sha256File = %q, want %q", got, want)
	}
}

func TestSha256Bytes(t *testing.T) {
	got := Sha256Bytes([]byte("abc\n"))
	want := "edeaaff3f1774ad2888673770c6d64097e391bc362d7d6fb34982ddf0efd18cb"
	if got != want {
		t.Errorf("Sha256Bytes = %q, want %q", got, want)
	}
}

func TestBuildFromDir_OverwritesExisting(t *testing.T) {
	// Build once, build again to a different output — should not error.
	dir := buildFixtureDir(t)
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	if _, err := BuildFromDir(dir, zipPath); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildFromDir(dir, zipPath); err != nil {
		t.Fatalf("second build to same path should succeed: %v", err)
	}
	// Verify content is still correct.
	names, err := ListZipNames(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 4 {
		t.Errorf("expected ≥4 entries, got %d (%v)", len(names), names)
	}
}

func TestExtractSkillMD_InvalidZip(t *testing.T) {
	// Pass a non-zip file (just text).
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.zip")
	if err := os.WriteFile(bad, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := ExtractSkillMD(bad)
	if err == nil {
		t.Fatal("expected error opening non-zip file")
	}
}

func TestBuildFromDir_PreservesDirectoryEntries(t *testing.T) {
	dir := buildFixtureDir(t)
	zipPath := filepath.Join(t.TempDir(), "out.zip")
	if _, err := BuildFromDir(dir, zipPath); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	hasDir := func(name string) bool {
		for _, f := range zr.File {
			if f.Name == name || f.Name == strings.TrimSuffix(name, "/") {
				return f.FileInfo().IsDir()
			}
		}
		return false
	}
	for _, d := range []string{"scripts/", "references/", "assets/"} {
		if !hasDir(d) {
			t.Errorf("missing directory entry %q in zip", d)
		}
	}
}

func TestExtractAllEntries_NoSkILLRequired(t *testing.T) {
	// A zip with only data files (no SKILL.md) should still extract OK
	// for the install path's purposes — we just won't be able to parse a
	// manifest from it. ExtractAllEntries doesn't require SKILL.md.
	zipPath := filepath.Join(t.TempDir(), "data.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("data.txt")
	w.Write([]byte("payload"))
	zw.Close()
	f.Close()
	destDir := t.TempDir()
	if err := ExtractAllEntries(zipPath, destDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destDir, "data.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "payload" {
		t.Errorf("data.txt = %q", string(data))
	}
}

// Ensure io.Discard is referenced so go vet stays quiet if io import is
// pulled for future use. (No-op for now.)
var _ = io.Discard
