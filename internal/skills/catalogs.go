package skills

// Catalog of allowed values for SKILL.md frontmatter fields introduced
// in schema v2.0:
//
//   - Categories: the skill's *functional* domain (what the skill does).
//     Distinct from internal/manifest.validCategories, which describes the
//     *agent type*. A "dev" skill (helper for coding) can ship inside an
//     "automation"-typed agent; the two vocabularies do not collapse.
//   - Agents: which downstream runtime recognizes the skill payload.
//     "all" is a sentinel meaning "no agent-specific install path; use the
//     central fallback". Combining "all" with concrete agents is rejected
//     by Validate.
//   - InstallMethods: how `agentpkg skills install` should materialize
//     the artifact on the target machine.
//
// Plus a default-path table for the per-agent install dirs (the real
// paths were verified against opencode.ai/docs/skills, the openclaw
// repo's docs/tools/skills.md, and hermes's hermes_constants.py).
//
// Documented in docs/skill-catalog.md.

// validCategories — skill functional domain. IDs are kebab-case-lowercase.
var validCategories = map[string]bool{
	"ops":          true, // 运维 / SRE / 监控
	"dev":          true, // 开发辅助 (debug / refactor / code review)
	"data":         true, // 数据处理 / 报表 / ETL
	"search":       true, // 检索 / RAG / 知识库
	"media":        true, // 图像 / 视频 / 音频
	"content":      true, // 内容创作 / 写作
	"integration":  true, // 第三方集成 (GitHub / Notion / Slack 等)
	"productivity": true, // 通用工具 / 效率
	"other":        true, // 兜底
}

// validAgents — runtime identifiers that recognize a SKILL.md payload.
// "all" is a sentinel: see IsAllAgents for the "all alone" rule.
var validAgents = map[string]bool{
	"opencode": true,
	"openclaw": true,
	"hermes":   true,
	"all":      true,
}

// validInstallMethods — the install pipelines we actually implement.
// "zip-extract" is the default for legacy zips; the others cover Python
// wheels (hermes-style), npm packages, and generic tarballs.
var validInstallMethods = map[string]bool{
	"zip-extract": true,
	"pip-wheel":   true,
	"npm-pack":    true,
	"tarball":     true,
}

// DefaultAgentInstallPaths — built-in agent → install-dir template.
//
// Each template uses $HOME and $NAME placeholders; expandPathTemplate
// substitutes them at install time. Override via SKILL.md's
// `metadata.install_paths.<agent>`.
//
// Sources:
//   - opencode:  opencode.ai/docs/skills (global location)
//   - openclaw:  agents/openclaw/.../docs/tools/skills.md (managed tier)
//   - hermes:    hermes_constants.py:get_optional_skills_dir()
//                (HERMES_HOME/optional-skills; HERMES_HOME defaults to
//                ~/.hermes on POSIX)
var DefaultAgentInstallPaths = map[string]string{
	"opencode": "$HOME/.config/opencode/skills/$NAME",
	"openclaw": "$HOME/.openclaw/skills/$NAME",
	"hermes":   "$HOME/.hermes/optional-skills/$NAME",
}

// FallbackPath — install target used when (a) agents=["all"] or (b)
// install_method is pip-wheel / npm-pack / tarball (system-wide).
// $HOME is expanded at runtime.
const FallbackPath = "$HOME/.local/share/agentpkg/skills/$NAME"

// IsValidCategory reports whether s is an allowed category ID.
func IsValidCategory(s string) bool { return validCategories[s] }

// IsValidAgent reports whether s is an allowed agent ID (incl. "all").
func IsValidAgent(s string) bool { return validAgents[s] }

// IsValidInstallMethod reports whether s is an allowed install_method.
func IsValidInstallMethod(s string) bool { return validInstallMethods[s] }

// IsAllAgents reports whether the agents list is the singleton ["all"].
// Per Validate, "all" cannot be combined with concrete agents, so the
// only "valid" all-form is the singleton.
//
// Returns false for nil/empty so callers can treat it as "not the all
// sentinel" — they then iterate per-agent paths themselves.
func IsAllAgents(agents []string) bool {
	return len(agents) == 1 && agents[0] == "all"
}

// KnownAgents returns the set of agent IDs (excludes "all", which is a
// sentinel rather than a real agent). Order is not significant — callers
// that need deterministic output should sort the result.
func KnownAgents() []string {
	out := make([]string, 0, len(validAgents))
	for a := range validAgents {
		if a == "all" {
			continue
		}
		out = append(out, a)
	}
	return out
}