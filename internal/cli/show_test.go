package cli

import (
	"strings"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
)

// TestFilterAgentVersions_NoFiltersReturnsAll covers the no-op path
// (--channel="" and --version="" → return every version). Guards
// against a regression where someone "tightens" the filter and
// accidentally drops versions when neither flag is set.
func TestFilterAgentVersions_NoFiltersReturnsAll(t *testing.T) {
	a := &apitypes.Agent{
		Name: "x",
		Versions: []apitypes.Version{
			{Version: "1.0.0", Channel: "stable"},
			{Version: "2.0.0-beta", Channel: "beta"},
		},
	}
	if err := filterAgentVersions(a, "", "", "x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.Versions) != 2 {
		t.Errorf("got %d versions, want 2", len(a.Versions))
	}
}

// TestFilterAgentVersions_ChannelOnly is the F010 regression:
//
// the previous version nested the channel check inside `if version !=
// ""`, so `show --channel beta` returned ALL stable versions. After
// the F010 fix the channel filter applies independently. This test
// guards against a future re-regression where someone re-nests them.
func TestFilterAgentVersions_ChannelOnly(t *testing.T) {
	a := &apitypes.Agent{
		Name: "x",
		Versions: []apitypes.Version{
			{Version: "1.0.0", Channel: "stable"},
			{Version: "2.0.0-beta", Channel: "beta"},
			{Version: "1.5.0-stable", Channel: "stable"},
		},
	}
	if err := filterAgentVersions(a, "beta", "", "x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.Versions) != 1 || a.Versions[0].Version != "2.0.0-beta" {
		t.Errorf("channel filter missed: got %+v", a.Versions)
	}
}

// TestFilterAgentVersions_VersionOnly matches one specific version.
func TestFilterAgentVersions_VersionOnly(t *testing.T) {
	a := &apitypes.Agent{
		Name: "x",
		Versions: []apitypes.Version{
			{Version: "1.0.0", Channel: "stable"},
			{Version: "1.5.0", Channel: "stable"},
			{Version: "2.0.0", Channel: "stable"},
		},
	}
	if err := filterAgentVersions(a, "", "1.5.0", "x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.Versions) != 1 || a.Versions[0].Version != "1.5.0" {
		t.Errorf("version filter missed: got %+v", a.Versions)
	}
}

// TestFilterAgentVersions_ChannelAndVersion filters by both — only
// versions matching BOTH the channel and the version string are kept.
func TestFilterAgentVersions_ChannelAndVersion(t *testing.T) {
	a := &apitypes.Agent{
		Name: "x",
		Versions: []apitypes.Version{
			{Version: "1.0.0", Channel: "stable"},
			{Version: "1.0.0", Channel: "beta"},
		},
	}
	if err := filterAgentVersions(a, "stable", "1.0.0", "x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.Versions) != 1 || a.Versions[0].Channel != "stable" {
		t.Errorf("got %+v, want only the stable 1.0.0", a.Versions)
	}
}

// TestFilterAgentVersions_NoMatchExitsNonZero is the F009 regression:
//
// the previous version silently produced an empty list when --channel
// or --version matched nothing, exit 0. Operators saw a "Versions:"
// header with zero entries and assumed the agent had no versions —
// they actually had a typo. We now exit non-zero and print the
// available versions so the caller can spot the typo immediately.
//
// This test asserts:
//   - error is non-nil
//   - error message lists the available versions
//   - error message has the typed exit code (ExitUsageError = 64) so
//     orchestrators can branch on it.
func TestFilterAgentVersions_NoMatchExitsNonZero(t *testing.T) {
	a := &apitypes.Agent{
		Name: "hermes-agent",
		Versions: []apitypes.Version{
			{Version: "0.19.0", Source: "upstream", Channel: "stable"},
			{Version: "0.20.0-beta", Source: "upstream", Channel: "beta"},
		},
	}

	// Version typo: 99.99.99 doesn't exist.
	err := filterAgentVersions(a, "", "99.99.99", "hermes-agent")
	if err == nil {
		t.Fatal("expected error for non-matching --version, got nil")
	}
	var ec ExitCoder
	if !errorsAs(err, &ec) {
		t.Errorf("error should implement ExitCoder (so main.go can os.Exit(64)); got %T", err)
	} else if ec.ExitCode() != ExitUsageError {
		t.Errorf("exit code = %d, want %d (ExitUsageError)", ec.ExitCode(), ExitUsageError)
	}
	if !strings.Contains(err.Error(), "no versions match") {
		t.Errorf("error message should say 'no versions match': %v", err)
	}
	if !strings.Contains(err.Error(), "0.19.0") || !strings.Contains(err.Error(), "0.20.0-beta") {
		t.Errorf("error message should list available versions; got: %v", err)
	}
}

// TestFilterAgentVersions_NoMatchChannelAlsoExitsNonZero covers the
// F009 case where --channel matches nothing (typo in channel name).
func TestFilterAgentVersions_NoMatchChannelAlsoExitsNonZero(t *testing.T) {
	a := &apitypes.Agent{
		Name: "hermes-agent",
		Versions: []apitypes.Version{
			{Version: "0.19.0", Source: "upstream", Channel: "stable"},
		},
	}
	err := filterAgentVersions(a, "dev", "", "hermes-agent")
	if err == nil {
		t.Fatal("expected error for non-matching --channel, got nil")
	}
	if !strings.Contains(err.Error(), "no versions match") {
		t.Errorf("error message should say 'no versions match': %v", err)
	}
	if !strings.Contains(err.Error(), "0.19.0") {
		t.Errorf("error message should list the available stable version; got: %v", err)
	}
}