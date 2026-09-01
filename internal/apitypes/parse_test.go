package apitypes

import (
	"testing"
)

// TestParseIndex_RoundTrip verifies that a minimal-but-realistic index.json
// decodes correctly into the typed Index struct and the Strip() view
// preserves the high-level fields while dropping per-version manifests.
func TestParseIndex_RoundTrip(t *testing.T) {
	raw := []byte(`{
        "generated_at": "2026-07-22T00:00:00Z",
        "schema_version": "1.0",
        "agents": [
            {
                "name": "opencode",
                "display_name": "OpenCode",
                "description": "AI coding agent",
                "logo": "https://example.com/opencode.svg",
                "category": "developer",
                "tags": ["ai", "code"],
                "versions": [
                    {
                        "version": "0.0.55",
                        "source": "upstream",
                        "channel": "stable",
                        "tarball": {
                            "filename": "opencode-upstream-0.0.55.tar.gz",
                            "size_bytes": 5981,
                            "sha256": "sha256:deadbeef"
                        },
                        "manifest": {
                            "schema_version": "1.0",
                            "agent": "opencode",
                            "source": "upstream",
                            "version": "0.0.55",
                            "channel": "stable",
                            "requires": {
                                "os": ["linux"],
                                "arch": ["x86_64"],
                                "system_tools": ["tar"]
                            },
                            "payload": [],
                            "checksums": {},
                            "tarball": {
                                "filename": "opencode-upstream-0.0.55.tar.gz",
                                "sha256": "sha256:deadbeef"
                            },
                            "upgrade": {
                                "strategy": "replace",
                                "compatible_from": [],
                                "migrations": []
                            },
                            "scripts": {
                                "install": "install.sh",
                                "uninstall": "uninstall.sh"
                            }
                        }
                    }
                ]
            }
        ]
    }`)

	idx, err := ParseIndex(raw)
	if err != nil {
		t.Fatalf("ParseIndex: %v", err)
	}
	if len(idx.Agents) != 1 {
		t.Fatalf("want 1 agent, got %d", len(idx.Agents))
	}
	a := idx.Agents[0]
	if a.Name != "opencode" || a.DisplayName != "OpenCode" {
		t.Errorf("agent meta wrong: %+v", a)
	}
	if len(a.Versions) != 1 {
		t.Fatalf("want 1 version, got %d", len(a.Versions))
	}
	v := a.Versions[0]
	if v.Tarball.Filename != "opencode-upstream-0.0.55.tar.gz" {
		t.Errorf("tarball filename: %q", v.Tarball.Filename)
	}
	if v.Manifest.Agent != "opencode" {
		t.Errorf("manifest not embedded: %+v", v.Manifest)
	}

	// Strip() must drop per-version manifests but keep everything else.
	s := idx.Strip()
	if len(s.Agents) != 1 {
		t.Fatalf("stripped.Agents: want 1 got %d", len(s.Agents))
	}
	if len(s.Agents[0].Versions) != 1 {
		t.Fatalf("stripped.Versions: want 1 got %d", len(s.Agents[0].Versions))
	}
}

// TestParseIndex_BadJSON checks that malformed input is rejected.
func TestParseIndex_BadJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"not-json", "this is not json"},
		{"trailing-garbage", `{"schema_version":"1.0"} extra junk`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseIndex([]byte(tc.raw)); err == nil {
				t.Errorf("expected error for %q", tc.name)
			}
		})
	}
}

// TestNewError verifies the structured-error JSON shape used by every API
// handler (and asserted by the CLI's error-decoding paths).
func TestNewError(t *testing.T) {
	e := NewError("not_found", "agent foo not found")
	if e.Error.Code != "not_found" {
		t.Errorf("code: %q", e.Error.Code)
	}
	if e.Error.Message == "" {
		t.Errorf("message is empty")
	}
}
