package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadCredentials_RoundTrip writes a credentials file in the format
// doLogin produces and verifies loadCredentials reads it back unchanged.
// This is the security-critical "password lands in $HOME/.config and is
// read back for every later command" path.
func TestLoadCredentials_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "credentials")
	if err := os.WriteFile(p, []byte("password: hunter2-secret\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := loadCredentials(p)
	if err != nil {
		t.Fatalf("loadCredentials: %v", err)
	}
	if got != "hunter2-secret" {
		t.Errorf("got %q, want %q", got, "hunter2-secret")
	}
}

// TestLoadCredentials_RejectsEmpty makes sure a config with an empty
// password (which would silently authenticate anonymously with some
// servers) is rejected up front.
func TestLoadCredentials_RejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		"password: \"\"\n",
		"password:\n",
		"other_field: x\n",
	}
	for i, raw := range cases {
		p := filepath.Join(dir, "creds_"+string(rune('a'+i)))
		if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := loadCredentials(p); err == nil {
			t.Errorf("case %d (%q): expected error, got nil", i, raw)
		}
	}
}

// TestLoadCredentials_MissingFile fails clearly when the file doesn't
// exist (so a fresh-install `agentpkg install` fails with a hint, not a
// confusing parse error).
func TestLoadCredentials_MissingFile(t *testing.T) {
	if _, err := loadCredentials(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected error for missing file")
	}
}

// TestLoadConfig_RequiresServer ensures that a config without a `server`
// URL is rejected before any HTTP attempt — guards against typos that
// would otherwise produce a confusing "connect: invalid URL" later.
func TestLoadConfig_RequiresServer(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("server: \"\"\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadConfig(p); err == nil {
		t.Error("expected error for missing server URL")
	}
	if err := os.WriteFile(p, []byte("not-a-url\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadConfig(p); err == nil {
		t.Error("expected error for malformed URL")
	}
}

// TestReadPassword_FromFile exercises the --password-file branch (the
// recommended path for non-interactive use in CI / scripts).
func TestReadPassword_FromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pw")
	if err := os.WriteFile(p, []byte("from-file-secret\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readPassword("", false, p)
	if err != nil {
		t.Fatalf("readPassword: %v", err)
	}
	if got != "from-file-secret" {
		t.Errorf("got %q", got)
	}
}

// TestReadPassword_FromArg exercises the --password branch (used by the
// agentpkg daemon when it can't drop a file or pipe stdin — accepts the
// password directly as a flag value).
func TestReadPassword_FromArg(t *testing.T) {
	got, err := readPassword("from-arg-secret", false, "")
	if err != nil {
		t.Fatalf("readPassword: %v", err)
	}
	if got != "from-arg-secret" {
		t.Errorf("got %q", got)
	}
}

// TestReadPassword_TrimsCRLF ensures trailing newlines from the source
// file/strip don't sneak into the password (which would cause a confusing
// 401 vs a typo'd secret).
func TestReadPassword_TrimsCRLF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pw")
	if err := os.WriteFile(p, []byte("secret\r\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readPassword("", false, p)
	if err != nil {
		t.Fatalf("readPassword: %v", err)
	}
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("password has trailing CR/LF: %q", got)
	}
	if got != "secret" {
		t.Errorf("got %q", got)
	}
}

// TestReadPassword_RejectsEmpty covers the "operator passed --password-file
// with no contents" failure mode.
func TestReadPassword_RejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pw")
	if err := os.WriteFile(p, []byte(""), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := readPassword("", false, p); err == nil {
		t.Error("expected error for empty password file")
	}
}

// TestReadPassword_PasswordBeatsStdin covers the documented priority order
// when multiple sources are passed in by mistake: --password wins so the
// caller gets a deterministic result (the cobra-level RunE rejects the
// combination before reaching here, but defense in depth).
func TestReadPassword_PasswordBeatsStdin(t *testing.T) {
	got, err := readPassword("literal", true, "")
	if err != nil {
		t.Fatalf("readPassword: %v", err)
	}
	if got != "literal" {
		t.Errorf("got %q, want %q (--password must win over --password-stdin)", got, "literal")
	}
}

// TestLoginCmd_RejectsMultiplePasswordSources hands the cobra cmd
// combinations of --password / --password-stdin / --password-file and
// verifies the cmd rejects (>1) with a clear error before any HTTP call.
func TestLoginCmd_RejectsMultiplePasswordSources(t *testing.T) {
	cases := [][]string{
		{"--password", "a", "--password-stdin"},
		{"--password", "a", "--password-file", "/tmp/x"},
		{"--password-stdin", "--password-file", "/tmp/x"},
	}
	for _, args := range cases {
		// We bypass the network: point --server at an unreachable host so
		// that if the cmd DOES try to call doLogin, the test fails for the
		// right reason (it should never reach there).
		full := append([]string{
			"--server", "http://127.0.0.1:1",
		}, args...)
		// stdin would otherwise block; the cmd should reject before reading.
		cmd := NewLoginCmd(new(string), new(string))
		cmd.SetArgs(full)
		cmd.SetIn(strings.NewReader(""))
		cmd.SetOut(&strings.Builder{})
		cmd.SetErr(&strings.Builder{})
		err := cmd.Execute()
		if err == nil {
			t.Errorf("args %v: expected error, got nil", args)
			continue
		}
		if !strings.Contains(err.Error(), "pass only one of") {
			t.Errorf("args %v: error %q does not mention mutual-exclusion", args, err.Error())
		}
	}
}
