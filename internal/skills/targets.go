package skills

import (
	"fmt"
)

// ResolveInstallTargets computes the concrete agents a skill is being
// installed for.
//
// declared is the skill's audience, straight from SKILL.md's `agents:`
// field. requested is the caller's agent selection (the CLI's --agents
// flag), possibly empty.
//
//   - A concrete audience (e.g. [opencode, hermes]) means "only these
//     runtimes support this skill". The target is that audience narrowed
//     by the caller's selection — the author is authoritative, so a
//     runtime that doesn't declare support never receives the skill.
//   - The "all" audience means "every runtime supports this skill". It
//     names no destination: the target is whatever the caller asked for,
//     and with no selection every known runtime is a target.
//
// The result always holds concrete agent IDs — never the "all" sentinel.
// "all" is an audience, not an install target: the payload lands in a
// concrete runtime's own skills directory, which is the only place that
// runtime looks for skills.
func ResolveInstallTargets(declared, requested []string) ([]string, error) {
	want := make(map[string]bool, len(requested))
	for _, raw := range requested {
		id := ResolveAgentID(raw)
		if !IsConcreteAgent(id) {
			return nil, fmt.Errorf("skills: requested agent %q is not a concrete agent", raw)
		}
		want[id] = true
	}

	if !IsAllAgents(declared) {
		out := make([]string, 0, len(declared))
		for _, a := range declared {
			if !IsConcreteAgent(a) {
				return nil, fmt.Errorf("skills: declared agent %q is not a concrete agent", a)
			}
			if len(want) > 0 && !want[a] {
				continue
			}
			out = append(out, a)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("--agents filter %v matched none of SKILL.md agents %v",
				requested, declared)
		}
		return out, nil
	}

	// Audience is "all" — the caller's selection decides the target.
	known := KnownAgents()
	if len(want) == 0 {
		return known, nil
	}
	out := make([]string, 0, len(want))
	for _, a := range known {
		if want[a] {
			out = append(out, a)
		}
	}
	return out, nil
}
