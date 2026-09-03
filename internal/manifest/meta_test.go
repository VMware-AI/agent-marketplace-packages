package manifest

import (
	"strings"
	"testing"
)

// TestMeta_Validate_RuntimeType locks in the runtime_type enum. The
// field is optional (omitted → Defaults() fills in "vm"), but when
// present must be one of the three known values. This test pins both
// behaviors so a future edit can't silently widen or narrow the set.
func TestMeta_Validate_RuntimeType(t *testing.T) {
	base := Meta{
		Description: "ok description here",
		Category:    "developer",
	}
	cases := []struct {
		name    string
		value   string
		wantErr bool
		wantMsg string
	}{
		{"vm", "vm", false, ""},
		{"container", "container", false, ""},
		{"k8s", "k8s", false, ""},
		{"empty_passes_validation", "", false, ""}, // empty OK; Defaults() will fill "vm"
		{"docker_invalid", "docker", true, `runtime_type "docker" is not valid`},
		{"case_sensitive", "VM", true, `runtime_type "VM" is not valid`},
		{"leading_space_invalid", " vm", true, `runtime_type " vm" is not valid`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.RuntimeType = tc.value
			err := m.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestMeta_Defaults_RuntimeType verifies that an empty runtime_type is
// filled in with "vm" by Defaults(). This is the contract that lets
// older meta.yaml files (predating this field) keep working without
// modification.
func TestMeta_Defaults_RuntimeType(t *testing.T) {
	m := &Meta{Description: "ok description here", Category: "developer"}
	m.Defaults("anything")
	if m.RuntimeType != "vm" {
		t.Errorf("Defaults(): RuntimeType = %q, want %q", m.RuntimeType, "vm")
	}
}

// TestMeta_Defaults_RuntimeType_NotOverridden verifies that an
// explicitly-set runtime_type is preserved through Defaults().
func TestMeta_Defaults_RuntimeType_NotOverridden(t *testing.T) {
	m := &Meta{
		Description:  "ok description here",
		Category:    "developer",
		RuntimeType: "k8s",
	}
	m.Defaults("anything")
	if m.RuntimeType != "k8s" {
		t.Errorf("Defaults(): RuntimeType = %q, want %q (explicit value should be preserved)", m.RuntimeType, "k8s")
	}
}