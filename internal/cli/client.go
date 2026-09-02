package cli

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client wraps http.Client with the marketplace-api base URL and the
// Basic Auth credentials loaded from disk.
type Client struct {
	BaseURL  string
	Password string
	HTTP     *http.Client
}

// NewClient reads the agentpkg config.yaml + credentials file and returns
// a configured Client. Returns an error if either is missing or malformed.
func NewClient(configPath, credsPath string) (*Client, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	password, err := loadCredentials(credsPath)
	if err != nil {
		return nil, fmt.Errorf("load credentials: %w", err)
	}
	httpClient, err := newHTTPClient(cfg.SkipCertVerify, cfg.CACert, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if cfg.SkipCertVerify {
		fmt.Fprintf(os.Stderr,
			"WARN: TLS certificate verification is disabled (--skip-cert-verify from login) — connections to %s are not authenticated\n",
			cfg.Server)
	}
	return &Client{
		BaseURL:  strings.TrimRight(cfg.Server, "/"),
		Password: password,
		HTTP:     httpClient,
	}, nil
}

// configShape is the subset of config.yaml we care about.
type configShape struct {
	Server         string `yaml:"server"`
	SkipCertVerify bool   `yaml:"skip_cert_verify,omitempty"`
	CACert         string `yaml:"ca_cert,omitempty"`
}

func loadConfig(path string) (*configShape, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c configShape
	if err := yamlUnmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Server == "" {
		return nil, fmt.Errorf("%s: server URL is required", path)
	}
	if _, err := url.Parse(c.Server); err != nil {
		return nil, fmt.Errorf("%s: invalid server URL: %w", path, err)
	}
	return &c, nil
}

// credentialsShape is the credentials file format (single password field).
type credentialsShape struct {
	Password string `yaml:"password"`
}

func loadCredentials(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var c credentialsShape
	if err := yamlUnmarshal(data, &c); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Password == "" {
		return "", fmt.Errorf("%s: password is empty", path)
	}
	return c.Password, nil
}

// do performs an authenticated GET and parses JSON into v.
func (c *Client) do(path string, v any) error {
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("agentpkg", c.Password)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("authentication failed (401) — run `agentpkg login`")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if v == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// yamlUnmarshal is a thin wrapper to keep yaml imports localized.
func yamlUnmarshal(data []byte, v any) error {
	return yamlUnmarshalImpl(data, v)
}

// tlsConfig builds a *tls.Config honoring both skip-verify and a custom
// CA bundle. Both can be set together (debug-mode + enterprise CA).
//
//   - skipVerify=true → InsecureSkipVerify: true (no chain validation)
//   - caPath != ""    → load the PEM bundle and add it to RootCAs
//
// Returns an error when caPath is set but the file is missing or contains
// no valid PEM certificates.
func tlsConfig(skipVerify bool, caPath string) (*tls.Config, error) {
	c := &tls.Config{InsecureSkipVerify: skipVerify}
	if caPath == "" {
		return c, nil
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA bundle %s: %w", caPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA bundle %s: no valid PEM certificates", caPath)
	}
	c.RootCAs = pool
	return c, nil
}

// newHTTPClient returns an *http.Client whose transport reflects the
// operator's TLS choices. timeout is per-call (login uses 10s, the
// persistent client used by whoami/index/show/download/etc. uses 30s).
//
// Errors from tlsConfig propagate — e.g. a CA bundle that fails to load
// surfaces as a NewClient error rather than a confusing handshake failure
// at the first HTTPS request.
func newHTTPClient(skipVerify bool, caPath string, timeout time.Duration) (*http.Client, error) {
	tc, err := tlsConfig(skipVerify, caPath)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: tc},
	}, nil
}
