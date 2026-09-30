package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewLoginCmd creates `agentpkg login`.
//
// Usage:
//
//	agentpkg login --server https://marketplace.example.com [--password <value>] \
//	               [--skip-cert-verify] [--ca-cert <pem>]
//
// Behavior:
//   - GETs /api/v1/health to verify connectivity + auth.
//   - On success: writes credentials (mode 0600) and saves the server URL
//     to the config file.
//   - On failure: returns the HTTP status + body for diagnosis.
//
// Password sources are mutually exclusive: --password, --password-stdin,
// --password-file, or the interactive prompt (when none are given). Passing
// more than one of the three explicit flags is rejected.
//
// TLS options (--skip-cert-verify, --ca-cert) are persisted to
// config.yaml and reused by all subsequent commands (whoami / index /
// show / download / install / upgrade / rollback). They do not need to
// be passed again.
func NewLoginCmd(cfgPath, credsPath *string) *cobra.Command {
	var (
		server         string
		password       string
		passwordStdin  bool
		passwordFile   string
		skipCertVerify bool
		caCert         string
	)
	c := &cobra.Command{
		Use:   "login --server <URL> [--password <value> | --password-stdin | --password-file <file>]",
		Short: "Authenticate to a marketplace-api and save credentials",
		Long: `login verifies connectivity + credentials by calling GET /api/v1/health,
then writes the server URL and password to disk for subsequent commands.

Password sources, mutually exclusive (priority: --password > --password-stdin
> --password-file > interactive prompt):

  --password <value>   pass the password directly (visible in the process
                       table; intended for the agentpkg daemon and similar
                       managed callers — prefer --password-file or
                       --password-stdin in shell scripts)
  --password-stdin     read the password from stdin (recommended for scripts).
                       Trailing whitespace (newlines, tabs, spaces, CR) is
                       trimmed — feed the bare password, e.g.
                           printf '%s' "$password" | agentpkg login ...
                       Copy-pasting with stray whitespace on the end will be
                       stripped silently and may produce a wrong-password error.
  --password-file <f>  read the password from a file. The file MUST be mode
                       0600 or 0400 — looser permissions are refused with an
                       error so we don't write a world-readable secret into
                       the credentials file. Use 'chmod 0600 <file>' first.
  (none)               prompt interactively from /dev/tty

TLS options (persisted to config.yaml; consumed by all subsequent commands):

  --skip-cert-verify   skip TLS certificate verification when talking to
                       the marketplace-api (INSECURE — connections are
                       not authenticated against the server's certificate
                       chain). Use only for self-signed test deployments.
                       A warning is printed on every subsequent agentpkg
                       invocation that inherits this setting, but only
                       for https:// servers — http:// URLs are plaintext
                       regardless of this flag and don't deserve the TLS
                       warning noise.
  --ca-cert <path>     path to a PEM-encoded CA bundle used to verify the
                       marketplace-api's certificate. Use for private-PKI
                       deployments where the root CA is not in the system
                       trust store. Saved as an absolute path.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if server == "" {
				return fmt.Errorf("--server is required (or set 'server' in config.yaml)")
			}
			picked := 0
			if password != "" {
				picked++
			}
			if passwordStdin {
				picked++
			}
			if passwordFile != "" {
				picked++
			}
			if picked > 1 {
				return fmt.Errorf("pass only one of --password, --password-stdin, --password-file")
			}
			pw, err := readPassword(password, passwordStdin, passwordFile)
			if err != nil {
				return err
			}
			// Resolve --ca-cert to an absolute path up front so the value
			// saved into config.yaml is stable across cd's, and validate
			// the file here so we don't write a config that would fail
			// later on every subsequent command.
			caCertAbs := ""
			if caCert != "" {
				abs, err := filepath.Abs(caCert)
				if err != nil {
					return fmt.Errorf("resolve --ca-cert path: %w", err)
				}
				if _, err := tlsConfig(skipCertVerify, abs); err != nil {
					return fmt.Errorf("--ca-cert: %w", err)
				}
				caCertAbs = abs
			}
			return doLogin(server, pw, *cfgPath, *credsPath, skipCertVerify, caCertAbs)
		},
	}
	c.Flags().StringVar(&server, "server", "", "marketplace-api base URL, e.g. https://marketplace.example.com")
	c.Flags().StringVar(&password, "password", "", "pass the password directly (mutually exclusive with --password-stdin and --password-file)")
	c.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read password from stdin (trailing whitespace is trimmed)")
	c.Flags().StringVar(&passwordFile, "password-file", "", "read password from a file (MUST be mode 0600 or 0400)")
	c.Flags().BoolVar(&skipCertVerify, "skip-cert-verify", false, "skip TLS certificate verification for the marketplace-api (INSECURE; persisted to config.yaml)")
	c.Flags().StringVar(&caCert, "ca-cert", "", "path to PEM CA bundle for the marketplace-api (persisted to config.yaml)")
	return c
}

// doLogin performs the actual login: GET /health with Basic Auth, and on
// success writes the credentials file and the server URL to the config.
//
// skipCertVerify and caCertAbs mirror the --skip-cert-verify / --ca-cert
// flags from the cobra command. When set, they are also written to
// config.yaml so that subsequent commands reuse the same TLS settings.
// When neither is set, any prior TLS configuration in config.yaml is
// cleared (so a re-login without the flags returns the operator to a
// hardened default).
func doLogin(server, password, cfgPath, credsPath string, skipCertVerify bool, caCertAbs string) error {
	// Probe connectivity + auth.
	req, err := http.NewRequest(http.MethodGet, server+"/api/v1/health", nil)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	req.SetBasicAuth("agentpkg", password)
	client, err := newHTTPClient(skipCertVerify, caCertAbs, 10*time.Second)
	if err != nil {
		return err
	}
	// F006 (tested 2026-09-30): the previous --skip-cert-verify warning
	// fired for every server URL, including plaintext http:// — where
	// TLS is not in play and the message is just noise. Suppress for
	// non-https URLs so an operator who logged into a development
	// http://localhost server doesn't see misleading "TLS
	// verification is disabled" warnings on every subsequent
	// whoami / index / install call. We keep the warning for https://
	// because that's the case where the user is consciously opting
	// out of certificate validation against an authenticated server.
	if skipCertVerify && isHTTPS(server) {
		fmt.Fprintf(os.Stderr,
			"WARN: TLS certificate verification is disabled for %s (--skip-cert-verify) — connections will not be authenticated\n",
			server)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", server, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("authentication failed (wrong password?)")
	}
	if resp.StatusCode != http.StatusOK {
		var body json.RawMessage
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return fmt.Errorf("health check returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Save credentials (mode 0600).
	if err := os.MkdirAll(filepath.Dir(credsPath), 0700); err != nil {
		return fmt.Errorf("mkdir credentials dir: %w", err)
	}
	creds := map[string]string{"password": password}
	credsData, err := yaml.Marshal(creds)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	if err := os.WriteFile(credsPath, credsData, 0600); err != nil {
		return fmt.Errorf("write credentials file: %w", err)
	}

	// Save (or update) the config file with the server URL + TLS settings.
	// map[string]any so we can mix the string `server` with the bool
	// `skip_cert_verify` and string `ca_cert`. Omitting the TLS keys when
	// not requested intentionally clears any prior value — "last login
	// wins" semantics.
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	cfg := map[string]any{"server": server}
	if skipCertVerify {
		cfg["skip_cert_verify"] = true
	}
	if caCertAbs != "" {
		cfg["ca_cert"] = caCertAbs
	}
	cfgData, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(cfgPath, cfgData, 0600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	fmt.Printf("Logged in to %s\n", server)
	fmt.Printf("  config:     %s\n", cfgPath)
	fmt.Printf("  credentials: %s\n", credsPath)
	return nil
}

// readPassword returns the password from one of the explicit sources
// (password literal, stdin, or file) or falls back to an interactive /dev/tty
// prompt when none are provided. The three explicit sources are mutually
// exclusive — the caller is expected to have enforced that before reaching
// this function.
func readPassword(password string, stdin bool, file string) (string, error) {
	switch {
	case password != "":
		if len(password) == 0 {
			return "", fmt.Errorf("empty password from --password")
		}
		return password, nil
	case stdin:
		data, err := readAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		// F003 (tested 2026-09-30): the previous version only stripped
		// trailing '\n' / '\r', leaving trailing tabs / spaces intact.
		// That was a footgun for shell pipelines where the operator
		// copy-pastes (or pipes) a password with stray whitespace on
		// the end — the resulting wrong-but-non-empty password was
		// silently written to disk and only surfaced as a 401 on the
		// next agentpkg command. Trim the full set of common
		// whitespace characters now. If a password genuinely ends in a
		// space, the operator must use --password directly.
		data = bytes.TrimRight(data, "\r\n\t ")
		if len(data) == 0 {
			return "", fmt.Errorf("empty password from stdin")
		}
		return string(data), nil
	case file != "":
		// F008 (tested 2026-09-30): the previous version accepted any
		// file mode, including 0664 / 0644 / world-readable, even
		// though the --password-file help text said "chmod 0600". An
		// operator who skipped the chmod wrote a world-readable
		// password into ~/.config/agentpkg/credentials. Refuse
		// anything looser than 0600 (or 0400, owner-only-read) up
		// front. Group and other bits must both be zero.
		info, err := os.Stat(file)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		if perms := info.Mode().Perm(); perms&0o077 != 0 {
			return "", fmt.Errorf("password file %s is mode %#o (must be 0600 or 0400 — group/other bits must be zero). chmod 0600 %s and retry", file, perms, file)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		data = bytes.TrimRight(data, "\r\n\t ")
		if len(data) == 0 {
			return "", fmt.Errorf("empty password from file")
		}
		return string(data), nil
	default:
		// Interactive prompt — read from /dev/tty if available, else stdin.
		fmt.Fprint(os.Stderr, "Password: ")
		data, err := readSecret()
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return string(data), nil
	}
}

// isHTTPS reports whether the given URL has the https:// scheme. Used by
// the --skip-cert-verify warning path (F006) to suppress the warning for
// plaintext http:// servers where TLS is not in play. Falls back to false
// on parse errors — a malformed URL is going to fail elsewhere anyway,
// and we don't want a warning that points at a URL we can't even parse.
func isHTTPS(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "https")
}

// readSecret reads a password without echoing it.
// Tries /dev/tty first (so interactive use in scripts with piped stdin
// still works), falls back to stdin.
func readSecret() ([]byte, error) {
	if f, err := os.Open("/dev/tty"); err == nil {
		defer f.Close()
		return readLineFrom(f)
	}
	return readAll(os.Stdin)
}
