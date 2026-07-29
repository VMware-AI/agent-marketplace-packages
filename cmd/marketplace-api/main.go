// Command marketplace-api is the static read-only HTTP server that exposes
// the contents of dist/ as a JSON + tarball API. See docs/api-reference.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/VMware-AI/agent-marketplace-packages/internal/repo"
	"github.com/VMware-AI/agent-marketplace-packages/internal/server"
	"gopkg.in/yaml.v3"
)

// Config is the YAML configuration loaded from --config.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Auth    AuthConfig    `yaml:"auth"`
	Repo    RepoConfig    `yaml:"repo"`
	Logging LoggingConfig `yaml:"logging"`
}

type ServerConfig struct {
	Listen  string `yaml:"listen"`   // e.g. "0.0.0.0:8080"
	TLSCert string `yaml:"tls_cert"` // optional
	TLSKey  string `yaml:"tls_key"`  // optional
}

type AuthConfig struct {
	PasswordEnv string `yaml:"password_env"` // env var name holding the password
}

type RepoConfig struct {
	DistDir string `yaml:"dist_dir"` // path to dist/
}

type LoggingConfig struct {
	Level string `yaml:"level"` // debug | info | warn | error
}

func main() {
	configPath := flag.String("config", "/etc/agent-marketplace/config.yaml", "path to config YAML")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Env-var overrides — env wins over config.yaml so operators can do
	// "everything in compose/.env, no config file edit" if they want.
	// Each var is optional; if unset, the config-file value stands.
	applyEnvOverrides(cfg)

	password := os.Getenv(cfg.Auth.PasswordEnv)
	if password == "" {
		log.Fatalf("auth.password_env %q is empty or unset", cfg.Auth.PasswordEnv)
	}

	distDir := cfg.Repo.DistDir
	if distDir == "" {
		log.Fatalf("repo.dist_dir is required")
	}

	dist := repo.NewDir(distDir)

	// Load + validate index.json at startup. Fail-fast: any error here
	// means the dist/ directory is broken and we cannot serve traffic.
	idx, err := dist.LoadIndex()
	if err != nil {
		log.Fatalf("load index.json: %v", err)
	}
	if err := dist.Validate(idx); err != nil {
		log.Fatalf("validate dist/: %v", err)
	}
	log.Printf("loaded %d agent(s), %d total version(s) from %s",
		len(idx.Agents), totalVersions(idx), distDir)

	state := &server.State{Index: idx, Dist: dist}
	handler := server.NewRouter(state, password)

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second, // large enough for tarball streaming
	}

	// Graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.Printf("marketplace-api listening on %s", cfg.Server.Listen)
		var err error
		if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
			err = srv.ListenAndServeTLS(cfg.Server.TLSCert, cfg.Server.TLSKey)
		} else {
			log.Printf("WARNING: TLS not configured — running plaintext HTTP. Production must set tls_cert + tls_key.")
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

// applyEnvOverrides lets MARKETPLACE_API_LISTEN / _DIST_DIR / _LOG_LEVEL
// override the corresponding config.yaml values when set. Useful for
// docker-compose / k8s deployments where putting every value in the
// compose file is preferable to maintaining a separate config.yaml.
//
// Each var is opt-in: unset → keep config-file value. This means you can
// ship a config.yaml with defaults and override only the fields that
// vary per environment (port, log level) without touching the file.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("MARKETPLACE_API_LISTEN"); v != "" {
		cfg.Server.Listen = v
	}
	if v := os.Getenv("MARKETPLACE_API_DIST_DIR"); v != "" {
		cfg.Repo.DistDir = v
	}
	if v := os.Getenv("MARKETPLACE_API_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
	// TLS overrides — for deployments that mount certs into well-known
	// paths and want to choose the file names via env instead of config.
	if v := os.Getenv("MARKETPLACE_API_TLS_CERT"); v != "" {
		cfg.Server.TLSCert = v
	}
	if v := os.Getenv("MARKETPLACE_API_TLS_KEY"); v != "" {
		cfg.Server.TLSKey = v
	}
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	if c.Server.Listen == "" {
		return nil, fmt.Errorf("server.listen is required")
	}
	return &c, nil
}

func totalVersions(idx *apitypes.Index) int {
	n := 0
	for _, a := range idx.Agents {
		n += len(a.Versions)
	}
	return n
}
