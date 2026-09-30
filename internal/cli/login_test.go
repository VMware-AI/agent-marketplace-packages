package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestReadPassword_TrimsAllWhitespace is the F003 regression:
//
// the previous version stripped only '\n' / '\r' from the password.
// Operators who copy-paste (or pipe through `echo`) the password with a
// stray tab / space at the end got a wrong-but-non-empty password
// written to disk — only surfacing as a 401 on the next agentpkg
// command. The fix trims the full whitespace set ("\r\n\t ") for both
// --password-stdin and --password-file.
func TestReadPassword_TrimsAllWhitespace(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"trailing_tab", "secret\t", "secret"},
		{"trailing_spaces", "secret   ", "secret"},
		{"trailing_mixed", "secret\r\n\t ", "secret"},
		{"no_trailing", "secret", "secret"},
		{"leading_kept", "  secret", "  secret"}, // leading whitespace is NOT trimmed
		{"internal_kept", "se cret", "se cret"},  // internal whitespace is NOT touched
	}
	for _, tc := range cases {
		t.Run("file_"+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "pw")
			if err := os.WriteFile(p, []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := readPassword("", false, p)
			if err != nil {
				t.Fatalf("readPassword: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
		t.Run("stdin_"+tc.name, func(t *testing.T) {
			// stdin's readPassword path is exercised via a pipe. We
			// close stdin with the bytes to read; readAll consumes
			// them. (Tests can't share os.Stdin globally — each run
			// is fine, but the test can't run in parallel with other
			// stdin tests.)
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			origStdin := os.Stdin
			os.Stdin = r
			defer func() { os.Stdin = origStdin }()
			if _, err := w.Write([]byte(tc.raw)); err != nil {
				t.Fatal(err)
			}
			w.Close()
			got, err := readPassword("", true, "")
			if err != nil {
				t.Fatalf("readPassword: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReadPassword_RejectsLooseFilePerms is the F008 regression:
//
// --password-file used to accept any file mode, including 0664 / 0644 /
// world-readable, even though the help text said "chmod 0600". An
// operator who skipped the chmod wrote a world-readable password into
// ~/.config/agentpkg/credentials. The fix refuses anything looser than
// 0600 (group/other bits must both be zero) up front, before any bytes
// are read.
func TestReadPassword_RejectsLooseFilePerms(t *testing.T) {
	cases := []struct {
		name    string
		mode    os.FileMode
		wantErr bool
	}{
		{"owner_only_rw_0600", 0600, false},
		{"owner_only_r_0400", 0400, false},
		{"group_readable_0640", 0640, true},
		{"world_readable_0644", 0644, true},
		{"group_writable_0660", 0660, true},
		{"world_writable_0666", 0666, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "pw")
			if err := os.WriteFile(p, []byte("secret\n"), tc.mode); err != nil {
				t.Fatal(err)
			}
			// os.WriteFile respects the umask; explicit chmod
			// afterwards so test mode matches tc.mode exactly even
			// when the runner's umask is wide.
			if err := os.Chmod(p, tc.mode); err != nil {
				t.Fatal(err)
			}
			_, err := readPassword("", false, p)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for mode %#o, got nil", tc.mode)
				} else if !strings.Contains(err.Error(), "must be 0600 or 0400") {
					t.Errorf("error message should mention 'must be 0600 or 0400', got: %v", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error for mode %#o: %v", tc.mode, err)
			}
		})
	}
}

// TestIsHTTPS exercises the scheme check that gates the F006 fix.
// We don't hit the network; we just want the function to agree with
// the obvious URL-string parsing.
func TestIsHTTPS(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://marketplace.example.com", true},
		{"HTTPS://marketplace.example.com", true}, // scheme is case-insensitive
		{"http://localhost:8080", false},
		{"HTTP://localhost", false},
		{"ftp://nope", false},
		{"", false},
		{"not a url", false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			if got := isHTTPS(tc.raw); got != tc.want {
				t.Errorf("isHTTPS(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
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

// --- TLS flag tests ----------------------------------------------------

// TestLoadConfig_ParsesTLSSettings writes a config.yaml containing both
// new fields and verifies loadConfig populates them. The omitempty tags
// on configShape must NOT swallow the bool=true / non-empty string on
// the way back in.
func TestLoadConfig_ParsesTLSSettings(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	caPath := "/etc/ssl/enterprise-ca.pem"
	yaml := "server: https://marketplace.example.com\n" +
		"skip_cert_verify: true\n" +
		"ca_cert: " + caPath + "\n"
	if err := os.WriteFile(p, []byte(yaml), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Server != "https://marketplace.example.com" {
		t.Errorf("server: got %q", cfg.Server)
	}
	if !cfg.SkipCertVerify {
		t.Errorf("SkipCertVerify: got false, want true")
	}
	if cfg.CACert != caPath {
		t.Errorf("CACert: got %q, want %q", cfg.CACert, caPath)
	}
}

// TestLoadConfig_TLSFieldsAbsent confirms a config with only `server:`
// yields zero values for the new fields. Operators who have never
// touched the TLS flags must not have their config suddenly interpreted
// as insecure.
func TestLoadConfig_TLSFieldsAbsent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("server: https://marketplace.example.com\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.SkipCertVerify {
		t.Errorf("SkipCertVerify: got true, want false")
	}
	if cfg.CACert != "" {
		t.Errorf("CACert: got %q, want \"\"", cfg.CACert)
	}
}

// writeSelfSignedPEM builds a one-off ECDSA self-signed cert in memory,
// encodes to PEM, and writes it to a temp file. Returns the path so the
// caller can hand it to tlsConfig / --ca-cert.
func writeSelfSignedPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "agentpkg-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageCertSign,
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	p := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(p, pemBytes, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// TestTLSConfig_LoadsPEMBundle generates a self-signed CA cert, hands
// the PEM to tlsConfig, and asserts the returned *tls.Config has a
// non-nil RootCAs. We don't introspect the pool directly — x509 doesn't
// expose its contents — but the absence of an error and a populated
// RootCAs field is sufficient.
func TestTLSConfig_LoadsPEMBundle(t *testing.T) {
	p := writeSelfSignedPEM(t)
	tc, err := tlsConfig(false, p)
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}
	if tc.RootCAs == nil {
		t.Errorf("RootCAs: got nil, want non-nil after loading %s", p)
	}
	if tc.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify: got true, want false")
	}
}

// TestTLSConfig_SkipVerify exercises the InsecureSkipVerify branch
// without a CA path — the default RootCAs stays nil (system pool used
// by Go's net/http anyway when TLSClientConfig.RootCAs is nil).
func TestTLSConfig_SkipVerify(t *testing.T) {
	tc, err := tlsConfig(true, "")
	if err != nil {
		t.Fatalf("tlsConfig: %v", err)
	}
	if !tc.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify: got false, want true")
	}
}

// TestTLSConfig_MissingFile confirms the helper fails clearly when the
// path passed via --ca-cert doesn't resolve — a confusing handshake
// error at the first HTTPS call is much harder to diagnose.
func TestTLSConfig_MissingFile(t *testing.T) {
	_, err := tlsConfig(false, filepath.Join(t.TempDir(), "no-such-ca.pem"))
	if err == nil {
		t.Error("expected error for missing CA file, got nil")
	}
}

// TestTLSConfig_BadPEM confirms the helper rejects a file that exists
// but doesn't contain valid PEM certificates — the AppendCertsFromPEM
// bool result is surfaced as an error so a typo'd --ca-cert doesn't
// silently fall back to system roots.
func TestTLSConfig_BadPEM(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "garbage.pem")
	if err := os.WriteFile(bad, []byte("not a certificate\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := tlsConfig(false, bad)
	if err == nil {
		t.Error("expected error for bad PEM, got nil")
	}
}

// TestNewClient_AppliesInsecureTransport writes a config with
// skip_cert_verify: true, calls NewClient, and asserts the resulting
// http.Client's transport is configured to skip TLS verification.
func TestNewClient_AppliesInsecureTransport(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credsPath := filepath.Join(dir, "creds")
	if err := os.WriteFile(cfgPath, []byte("server: https://marketplace.example.com\nskip_cert_verify: true\n"), 0600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	if err := os.WriteFile(credsPath, []byte("password: x\n"), 0600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	c, err := NewClient(cfgPath, credsPath)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tr, ok := c.HTTP.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTP.Transport: got %T, want *http.Transport", c.HTTP.Transport)
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify: got %v, want true", tr.TLSClientConfig)
	}
}

// TestNewClient_AppliesCACert writes a config with ca_cert, calls
// NewClient, and asserts the transport has a populated RootCAs pool.
// We don't compare cert contents (x509 doesn't expose them) — the
// "non-nil after config-driven load" assertion is sufficient.
func TestNewClient_AppliesCACert(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credsPath := filepath.Join(dir, "creds")
	caPath := writeSelfSignedPEM(t)
	yaml := "server: https://marketplace.example.com\nca_cert: " + caPath + "\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	if err := os.WriteFile(credsPath, []byte("password: x\n"), 0600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	c, err := NewClient(cfgPath, credsPath)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tr, ok := c.HTTP.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTP.Transport: got %T, want *http.Transport", c.HTTP.Transport)
	}
	if tr.TLSClientConfig == nil {
		t.Fatalf("TLSClientConfig: got nil")
	}
	if tr.TLSClientConfig.RootCAs == nil {
		t.Errorf("RootCAs: got nil, want non-nil after loading %s", caPath)
	}
}

// TestLoginCmd_PersistsSkipCertVerify exercises the cobra command end-
// to-end: pass --skip-cert-verify, point --server at an httptest server
// (with no TLS — we skip verify to prove the flag was honored), and
// assert the written config.yaml contains skip_cert_verify: true.
//
// This is the integration test for "flag → config.yaml" round-tripping.
func TestLoginCmd_PersistsSkipCertVerify(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credsPath := filepath.Join(dir, "creds")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"status":"ok","version":"test"}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	cmd := NewLoginCmd(&cfgPath, &credsPath)
	cmd.SetArgs([]string{
		"--server", srv.URL,
		"--password", "x",
		"--skip-cert-verify",
	})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("login: %v", err)
	}

	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read cfg: %v", err)
	}
	if !strings.Contains(string(cfgBytes), "skip_cert_verify: true") {
		t.Errorf("config.yaml missing skip_cert_verify: true; got:\n%s", cfgBytes)
	}
}

// TestLoginCmd_ReloginClearsTLS confirms the "last login wins" semantic:
// a second login without --skip-cert-verify clears the flag from
// config.yaml so an operator can return to a hardened default.
func TestLoginCmd_ReloginClearsTLS(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credsPath := filepath.Join(dir, "creds")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	// First login with the flag set.
	cmd1 := NewLoginCmd(&cfgPath, &credsPath)
	cmd1.SetArgs([]string{"--server", srv.URL, "--password", "x", "--skip-cert-verify"})
	cmd1.SetOut(&strings.Builder{})
	cmd1.SetErr(&strings.Builder{})
	if err := cmd1.Execute(); err != nil {
		t.Fatalf("first login: %v", err)
	}

	// Second login without it.
	cmd2 := NewLoginCmd(&cfgPath, &credsPath)
	cmd2.SetArgs([]string{"--server", srv.URL, "--password", "x"})
	cmd2.SetOut(&strings.Builder{})
	cmd2.SetErr(&strings.Builder{})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("second login: %v", err)
	}

	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read cfg: %v", err)
	}
	if strings.Contains(string(cfgBytes), "skip_cert_verify") {
		t.Errorf("config.yaml still contains skip_cert_verify after re-login without flag; got:\n%s", cfgBytes)
	}
}

// TestLoginCmd_BadCACertFailsBeforeWrite asserts that a --ca-cert path
// pointing at a malformed PEM produces a clear error AND does NOT
// write a config file — operators must not end up with a config that
// later fails every command.
func TestLoginCmd_BadCACertFailsBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credsPath := filepath.Join(dir, "creds")
	bad := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(bad, []byte("not a certificate\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := NewLoginCmd(&cfgPath, &credsPath)
	cmd.SetArgs([]string{
		"--server", "https://marketplace.example.com",
		"--password", "x",
		"--ca-cert", bad,
	})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for bad CA bundle, got nil")
	}
	if !strings.Contains(err.Error(), "CA bundle") {
		t.Errorf("error %q does not mention CA bundle", err.Error())
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("config.yaml should not be written on CA failure; stat err = %v", err)
	}
}

// TestLoginCmd_PersistsCACert exercises the cobra command with
// --ca-cert: the PEM path is written to config.yaml as an absolute
// path (so cd'ing later doesn't break it) and the actual file contents
// are not read by doLogin's HTTP probe — validation already happened
// in RunE.
func TestLoginCmd_PersistsCACert(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	credsPath := filepath.Join(dir, "creds")
	caPath := writeSelfSignedPEM(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	cmd := NewLoginCmd(&cfgPath, &credsPath)
	cmd.SetArgs([]string{
		"--server", srv.URL,
		"--password", "x",
		"--ca-cert", caPath,
	})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("login: %v", err)
	}

	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read cfg: %v", err)
	}
	if !strings.Contains(string(cfgBytes), "ca_cert:") {
		t.Errorf("config.yaml missing ca_cert entry; got:\n%s", cfgBytes)
	}
	// The path in config must be absolute so cd doesn't break it later.
	line := ""
	for _, l := range strings.Split(string(cfgBytes), "\n") {
		if strings.HasPrefix(l, "ca_cert:") {
			line = strings.TrimSpace(strings.TrimPrefix(l, "ca_cert:"))
			break
		}
	}
	if line == "" || !filepath.IsAbs(line) {
		t.Errorf("ca_cert in config is not absolute: %q", line)
	}
}
