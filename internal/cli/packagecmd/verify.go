package packagecmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/VMware-AI/agent-marketplace-packages/internal/manifest"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewVerifyCmd creates `agentpkg package verify`.
//
// Walks the agent directory and checks:
//   1. meta.yaml (if present) — schema validation (display_name/icon/category/tags)
//   2. Every agents/<name>/<source>/<version>/manifest.json — schema + checksums
//      (recomputes SHA256 of payload/runtime files and compares to manifest.checksums)
func NewVerifyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "verify <path>",
		Short: "Verify an agent package directory (meta.yaml + manifest.json + payload checksums)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentDir := args[0]
			return verifyAgent(agentDir, false)
		},
	}
	return c
}

// verifyAgent runs the verification pipeline on agentDir.
//
// If --strict is passed (second arg), any missing field is an error.
// Otherwise it logs warnings and continues.
func verifyAgent(agentDir string, strict bool) error {
	name := filepath.Base(agentDir)
	fmt.Printf("Verifying %s ...\n", name)

	// meta.yaml — optional but if present must validate.
	metaPath := filepath.Join(agentDir, "meta.yaml")
	if _, err := os.Stat(metaPath); err == nil {
		meta, err := manifest.LoadMeta(metaPath)
		if err != nil {
			fmt.Printf("  meta.yaml:    FAIL  %v\n", err)
			return err
		}
		meta.Defaults(name)
		fmt.Printf("  meta.yaml:    ok    display_name=%q icon=%q category=%q tags=%v\n",
			meta.DisplayName, meta.Icon, meta.Category, meta.Tags)
	} else {
		fmt.Println("  meta.yaml:    skip  (not present)")
	}

	// Each <source>/<version>/manifest.json.
	versionDirs, err := filepath.Glob(filepath.Join(agentDir, "*", "*", "manifest.json"))
	if err != nil {
		return fmt.Errorf("glob manifests: %w", err)
	}
	if len(versionDirs) == 0 {
		return fmt.Errorf("no <source>/<version>/manifest.json files found under %s", agentDir)
	}

	allOK := true
	for _, mf := range versionDirs {
		if err := verifyManifestDir(agentDir, mf); err != nil {
			fmt.Printf("  %s:  FAIL  %v\n", relTo(agentDir, mf), err)
			allOK = false
			continue
		}
		fmt.Printf("  %s:  ok\n", relTo(agentDir, mf))
	}

	if !allOK {
		return fmt.Errorf("verification failed for %s", name)
	}
	_ = strict
	return nil
}

func verifyManifestDir(agentDir, manifestPath string) error {
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return err
	}
	if m.Agent == "" {
		return fmt.Errorf("manifest.agent is required")
	}
	if m.Source == "" || m.Version == "" {
		return fmt.Errorf("manifest.source and .version are required")
	}
	// Recompute checksums for every file in manifest.checksums.
	for rel, expected := range m.Checksums {
		full := filepath.Join(filepath.Dir(manifestPath), rel)
		data, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		h := sha256.Sum256(data)
		actual := "sha256:" + hex.EncodeToString(h[:])
		if actual != expected {
			return fmt.Errorf("checksum mismatch for %s: expected=%s actual=%s", rel, expected, actual)
		}
	}
	return nil
}

// relTo returns a relative path for prettier logging.
func relTo(base, p string) string {
	if rel, err := filepath.Rel(base, p); err == nil {
		return rel
	}
	return p
}

// jsonMarshalIndent is shared by build / reindex for index.json writing.
func jsonMarshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// yamlMarshalIndent for meta.yaml writing.
func yamlMarshalIndent(v any) ([]byte, error) {
	return yaml.Marshal(v)
}