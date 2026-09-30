package skills

// Marketplace name ↔ agent ID translation.
//
// The marketplace-api lists agent packages under names like "hermes-agent",
// "openclaw", "opencode" — these are the names `agentpkg index` returns
// and the keys under which `state.json` records the installed agent.
// But the per-agent install paths in SKILL.md (`agents:` field,
// `DefaultAgentInstallPaths` keys) use shorter IDs that match the
// runtime's own identifier — "hermes" instead of "hermes-agent".
//
// Without a translation table, callers that want to install a skill
// "for the agent currently running on this VM" can't map the
// marketplace name (read from state.json) to the agent ID that
// `agentpkg skills install` filters against. This file holds that
// mapping.
//
// Add new entries here when a new agent is published under a
// marketplace name that doesn't match its runtime agent ID. Most agents
// (opencode, openclaw) have matching names and need no entry.

// marketplaceNameAliases maps marketplace-package-name → agent ID used
// in SKILL.md and DefaultAgentInstallPaths.
var marketplaceNameAliases = map[string]string{
	"hermes-agent": "hermes",
}

// ResolveAgentID returns the agent ID (the key used in `agents:` and
// `DefaultAgentInstallPaths`) for the given marketplace package name.
// Pass-through when no alias is registered — i.e. the marketplace name
// and the agent ID are identical, which is the common case.
func ResolveAgentID(marketplaceName string) string {
	if id, ok := marketplaceNameAliases[marketplaceName]; ok {
		return id
	}
	return marketplaceName
}

// IsKnownMarketplaceName reports whether the given string is either a
// known agent ID (per validAgents) or has a registered alias. Useful
// for validating CLI flag values like `--agents <name>`.
func IsKnownMarketplaceName(name string) bool {
	if validAgents[name] {
		return true
	}
	_, ok := marketplaceNameAliases[name]
	return ok
}