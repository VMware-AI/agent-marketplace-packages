package skills

import (
	"strings"
	"testing"
)

// TestResolveInstallTargets covers the audience-vs-target resolution:
// SKILL.md's `agents:` declares which runtimes SUPPORT a skill, while
// the caller's selection says which runtime(s) it is installed FOR.
func TestResolveInstallTargets(t *testing.T) {
	cases := []struct {
		name      string
		declared  []string
		requested []string
		want      []string
		wantErr   bool
	}{
		{
			name:     "concrete audience, no selection",
			declared: []string{"opencode", "hermes"},
			want:     []string{"opencode", "hermes"},
		},
		{
			name:      "concrete audience narrowed by the selection",
			declared:  []string{"opencode", "hermes"},
			requested: []string{"hermes"},
			want:      []string{"hermes"},
		},
		{
			name:      "concrete audience, selection matches none",
			declared:  []string{"opencode"},
			requested: []string{"hermes"},
			wantErr:   true,
		},
		{
			name:      "all audience resolved by the selection",
			declared:  []string{"all"},
			requested: []string{"opencode"},
			want:      []string{"opencode"},
		},
		{
			name:      "all audience, multiple agents selected",
			declared:  []string{"all"},
			requested: []string{"opencode", "hermes"},
			want:      []string{"hermes", "opencode"},
		},
		{
			name:     "all audience, no selection → every known agent",
			declared: []string{"all"},
			want:     []string{"hermes", "openclaw", "opencode"},
		},
		{
			name:      "marketplace alias resolved",
			declared:  []string{"all"},
			requested: []string{"hermes-agent"},
			want:      []string{"hermes"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveInstallTargets(tc.declared, tc.requested)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveInstallTargets: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
			// The result is always concrete — "all" is an audience,
			// never an install target.
			for _, a := range got {
				if !IsConcreteAgent(a) {
					t.Errorf("resolved target %q is not a concrete agent", a)
				}
			}
		})
	}
}

// TestResolveInstallTargets_RejectsTheAllSentinelAsATarget guards the
// invariant that keeps "all" from silently becoming an install path
// again: a caller can never resolve to the sentinel itself.
func TestResolveInstallTargets_RejectsTheAllSentinelAsATarget(t *testing.T) {
	if _, err := ResolveInstallTargets([]string{"all"}, []string{"all"}); err == nil {
		t.Fatal("expected an error when the selection itself is the \"all\" sentinel")
	} else if !strings.Contains(err.Error(), "not a concrete agent") {
		t.Errorf("unexpected error message: %v", err)
	}
}
