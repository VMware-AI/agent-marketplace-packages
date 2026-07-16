package cli

import (
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
	return &Client{
		BaseURL:  strings.TrimRight(cfg.Server, "/"),
		Password: password,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// configShape is the subset of config.yaml we care about.
type configShape struct {
	Server string `yaml:"server"`
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