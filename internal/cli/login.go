package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
  --password-stdin     read the password from stdin (recommended for scripts)
  --password-file <f>  read the password from a file (chmod 0600)
  (none)               prompt interactively from /dev/tty

TLS options (persisted to config.yaml; consumed by all subsequent commands):

  --skip-cert-verify   skip TLS certificate verification when talking to
                       the marketplace-api (INSECURE — connections are
                       not authenticated against the server's certificate
                       chain). Use only for self-signed test deployments.
                       Wires through to every later agentpkg invocation.
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
	c.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read password from stdin")
	c.Flags().StringVar(&passwordFile, "password-file", "", "read password from a file (chmod 0600)")
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
	if skipCertVerify {
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
		// Trim trailing newline if present.
		for len(data) > 0 && (data[len(data)-1] == '\n' || data[len(data)-1] == '\r') {
			data = data[:len(data)-1]
		}
		if len(data) == 0 {
			return "", fmt.Errorf("empty password from stdin")
		}
		return string(data), nil
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		for len(data) > 0 && (data[len(data)-1] == '\n' || data[len(data)-1] == '\r') {
			data = data[:len(data)-1]
		}
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
