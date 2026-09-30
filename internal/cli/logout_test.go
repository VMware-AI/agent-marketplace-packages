package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogout_PreservesConfigByDefault is the F001 regression:
//
// the previous logout silently kept config.yaml (server URL + TLS
// settings) after removing credentials. Operators who wanted a clean
// slate had no way to express that. We now (a) document the default
// in --help and the success message, (b) accept --clear-config as
// the explicit opt-out.
func TestLogout_PreservesConfigByDefault(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.yaml")
	creds := filepath.Join(tmp, "credentials")
	// Pre-populate both files (mode 0600 to match what login writes).
	if err := os.WriteFile(cfg, []byte("server: https://m.example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(creds, []byte("password: x\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Wire cobra pointers at our temp paths.
	cfgPath := cfg
	credsPath := creds
	cmd := NewLogoutCmd(&cfgPath, &credsPath)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logout: %v\nstderr: %s", err, errOut.String())
	}

	// Credentials should be gone; config should be preserved.
	if _, err := os.Stat(creds); !os.IsNotExist(err) {
		t.Errorf("credentials still present after logout: %v", err)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Errorf("config removed by default logout (should be preserved): %v", err)
	}
	if !strings.Contains(out.String(), "Server / TLS settings preserved") {
		t.Errorf("default logout should announce config preservation; got: %s", out.String())
	}
}

// TestLogout_ClearConfigWipesBoth confirms the explicit-opt-out path
// for F001: --clear-config removes both credentials and config.yaml so
// the next login starts from a clean slate (no --server / --ca-cert
// reuse from a prior session).
func TestLogout_ClearConfigWipesBoth(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.yaml")
	creds := filepath.Join(tmp, "credentials")
	if err := os.WriteFile(cfg, []byte("server: https://m.example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(creds, []byte("password: x\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfgPath := cfg
	credsPath := creds
	cmd := NewLogoutCmd(&cfgPath, &credsPath)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--clear-config"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("logout --clear-config: %v\nstderr: %s", err, errOut.String())
	}

	if _, err := os.Stat(creds); !os.IsNotExist(err) {
		t.Errorf("credentials still present: %v", err)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Errorf("config still present after --clear-config: %v", err)
	}
	if !strings.Contains(out.String(), "Cleared config.yaml") {
		t.Errorf("--clear-config should announce config clear; got: %s", out.String())
	}
}

// TestLogout_IdempotentMissing confirms that logout on a clean machine
// (no credentials, no config) is a no-op success rather than an error.
// Both --clear-config and the default path should be silent when
// there's nothing to remove.
func TestLogout_IdempotentMissing(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.yaml")
	creds := filepath.Join(tmp, "credentials")

	cfgPath := cfg
	credsPath := creds
	cmd := NewLogoutCmd(&cfgPath, &credsPath)

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Errorf("logout with no files should not error, got: %v", err)
	}
	if !strings.Contains(out.String(), "Already logged out") {
		t.Errorf("missing 'Already logged out' message: %s", out.String())
	}
}