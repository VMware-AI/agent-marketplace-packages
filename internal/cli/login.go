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
//	agentpkg login --server https://marketplace.example.com [--password-stdin]
//
// Behavior:
//   - GETs /api/v1/health to verify connectivity + auth.
//   - On success: writes credentials (mode 0600) and saves the server URL
//     to the config file.
//   - On failure: returns the HTTP status + body for diagnosis.
func NewLoginCmd(cfgPath, credsPath *string) *cobra.Command {
	var (
		server       string
		passwordStdin bool
		passwordFile  string
	)
	c := &cobra.Command{
		Use:   "login --server <URL> [--password-stdin | --password-file <file>]",
		Short: "Authenticate to a marketplace-api and save credentials",
		Long: `login verifies connectivity + credentials by calling GET /api/v1/health,
then writes the server URL and password to disk for subsequent commands.

By default the password is read interactively from the terminal. Pass
--password-stdin to read from stdin (recommended for scripts), or
--password-file to read from a specific file (chmod 0600).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if server == "" {
				return fmt.Errorf("--server is required (or set 'server' in config.yaml)")
			}
			password, err := readPassword(passwordStdin, passwordFile)
			if err != nil {
				return err
			}
			return doLogin(server, password, *cfgPath, *credsPath)
		},
	}
	c.Flags().StringVar(&server, "server", "", "marketplace-api base URL, e.g. https://marketplace.example.com")
	c.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read password from stdin")
	c.Flags().StringVar(&passwordFile, "password-file", "", "read password from a file (chmod 0600)")
	return c
}

// doLogin performs the actual login: GET /health with Basic Auth, and on
// success writes the credentials file and the server URL to the config.
func doLogin(server, password, cfgPath, credsPath string) error {
	// Probe connectivity + auth.
	req, err := http.NewRequest(http.MethodGet, server+"/api/v1/health", nil)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	req.SetBasicAuth("agentpkg", password)
	client := &http.Client{Timeout: 10 * time.Second}
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

	// Save (or update) the config file with the server URL.
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	cfg := map[string]string{"server": server}
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

func readPassword(stdin bool, file string) (string, error) {
	switch {
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