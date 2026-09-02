// Command marketplace-api is the static read-only HTTP server that exposes
// the contents of dist/ as a JSON + tarball API. See docs/api-reference.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/VMware-AI/agent-marketplace-packages/internal/repo"
	"github.com/VMware-AI/agent-marketplace-packages/internal/server"
	"github.com/VMware-AI/agent-marketplace-packages/internal/server/reload"
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
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
	File   string `yaml:"file"`   // optional log file path (append)
}

func main() {
	configPath := flag.String("config", "/etc/agent-marketplace/config.yaml", "path to config YAML")
	logFormat := flag.String("log-format", "", "log format: text|json (default: text; env MARKETPLACE_API_LOG_FORMAT or yaml logging.format also accepted)")
	logFile := flag.String("log-file", "", "optional log file path; logs go to both stdout and this file (env MARKETPLACE_API_LOG_FILE or yaml logging.file)")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		// Logger isn't built yet — fall back to plain stderr via stdlib.
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	// Env-var overrides — env wins over config.yaml so operators can do
	// "everything in compose/.env, no config file edit" if they want.
	// Each var is optional; if unset, the config-file value stands.
	applyEnvOverrides(cfg)

	// CLI flags win over env (most explicit input).
	if *logFormat != "" {
		cfg.Logging.Format = *logFormat
	}
	if *logFile != "" {
		cfg.Logging.File = *logFile
	}

	// Poll interval for the dist/ reload loop. SIGHUP always works; the
	// poll ticker is the automatic fallback for "operator forgot to send
	// HUP" or container environments where sending HUP is awkward. Env
	// override only (no YAML knob) — matches the convention for
	// operational tunables documented in applyEnvOverrides.
	pollInterval := 10 * time.Second
	if v := os.Getenv("MARKETPLACE_API_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse MARKETPLACE_API_POLL_INTERVAL=%q: %v\n", v, err)
			os.Exit(1)
		}
		pollInterval = d
	}

	logger, closeLog, err := server.NewLogger(cfg.Logging.Level, cfg.Logging.Format, cfg.Logging.File)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = closeLog()
	}()
	slog.SetDefault(logger)

	password := os.Getenv(cfg.Auth.PasswordEnv)
	if password == "" {
		logger.Error("auth.password_env is empty or unset", "env", cfg.Auth.PasswordEnv)
		os.Exit(1)
	}

	distDir := cfg.Repo.DistDir
	if distDir == "" {
		logger.Error("repo.dist_dir is required")
		os.Exit(1)
	}

	dist := repo.NewDir(distDir)

	// Capture the on-disk fingerprint BEFORE the initial load. If a reindex
	// lands between this stat and the LoadIndex below, the initial load will
	// pick up the new file — which is exactly what we want. See
	// internal/server/reload for the rationale.
	fp, _ := reload.InitialFingerprint(dist)

	// Load + validate index.json at startup. Fail-fast: any error here
	// means the dist/ directory is broken and we cannot serve traffic.
	idx, err := dist.LoadIndex()
	if err != nil {
		logger.Error("load index.json", "err", err)
		os.Exit(1)
	}
	if err := dist.Validate(idx); err != nil {
		logger.Error("validate dist/", "err", err)
		os.Exit(1)
	}
	logger.Info("dist loaded",
		"agents", len(idx.Agents),
		"versions", totalVersions(idx),
		"dist_dir", distDir,
		"poll_interval", pollInterval.String(),
	)

	state := server.NewState(dist, idx)
	handler := server.NewRouter(state, password, logger)

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second, // large enough for tarball streaming
	}

	// Graceful shutdown — SIGINT/SIGTERM only. SIGHUP is handled separately
	// below as a reload trigger; adding it here would treat reload as a
	// shutdown signal.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Reload trigger channel — SIGHUP wakes the reload goroutine for an
	// immediate re-read. Buffered so a misbehaving sender can never block.
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	go func() {
		logger.Info("marketplace-api listening", "addr", cfg.Server.Listen)
		var err error
		if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
			err = srv.ListenAndServeTLS(cfg.Server.TLSCert, cfg.Server.TLSKey)
		} else {
			logger.Warn("TLS not configured — running plaintext HTTP. Production must set tls_cert + tls_key.")
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	// Reload coordinator — selects on shutdown, SIGHUP, and the poll
	// ticker. pollInterval of 0 disables polling (SIGHUP-only).
	go runReloadLoop(ctx, sighup, dist, state, &fp, pollInterval, logger)

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "err", err)
	}
}

// runReloadLoop selects on shutdown, SIGHUP, and (optionally) a poll
// ticker. Each non-shutdown event triggers reload.MaybeReload, which
// itself no-ops when nothing has changed. The function never panics on
// reload failure — that's why we wire the loop in main rather than
// letting reload errors tear the process down.
func runReloadLoop(
	ctx context.Context,
	sighup <-chan os.Signal,
	dist *repo.Dir,
	state *server.State,
	fp *reload.Fingerprint,
	interval time.Duration,
	logger *slog.Logger,
) {
	if interval <= 0 {
		// SIGHUP-only mode.
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighup:
				if err := reload.MaybeReload(state, dist, fp, logger); err != nil {
					logger.Warn("manual reload", "err", err)
				}
			}
		}
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sighup:
			if err := reload.MaybeReload(state, dist, fp, logger); err != nil {
				logger.Warn("manual reload", "err", err)
			}
		case <-t.C:
			if err := reload.MaybeReload(state, dist, fp, logger); err != nil {
				// MaybeReload already logs the underlying error; this branch
				// is here so future code can hook on persistent failures.
				_ = err
			}
		}
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
	if v := os.Getenv("MARKETPLACE_API_LOG_FORMAT"); v != "" {
		cfg.Logging.Format = v
	}
	if v := os.Getenv("MARKETPLACE_API_LOG_FILE"); v != "" {
		cfg.Logging.File = v
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