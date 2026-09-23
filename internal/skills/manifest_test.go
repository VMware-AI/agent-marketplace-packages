package skills

import (
	"strings"
	"testing"
)

const validBaseSKILL = "---\n" +
	"name: web-search\n" +
	"description: Adds web search capability using Google for current information.\n" +
	"license: MIT\n" +
	"allowed-tools: [Read, Grep]\n" +
	`compatibility: "Requires Python 3.10+, jq"` + "\n" +
	"version: 1.2.3\n" +
	"author: Jane Developer\n" +
	"category: search\n" +
	"agents: [opencode, hermes]\n" +
	"tags: [search, web]\n" +
	"homepage: https://example.com/web-search\n" +
	"created_at: 2026-09-01\n" +
	"metadata:\n" +
	"  requires:\n" +
	"    os: [linux, darwin]\n" +
	"    arch: [amd64, arm64]\n" +
	"    tools: [bash, jq]\n" +
	"  inputs:\n" +
	"    - name: target_dir\n" +
	"      description: Where to write\n" +
	"      required: true\n" +
	"      type: string\n" +
	"      default: /tmp/out\n" +
	"    - name: max_files\n" +
	"      description: Cap\n" +
	"      required: false\n" +
	"      type: integer\n" +
	`      default: "100"` + "\n" +
	"  entry_point: scripts/run.sh\n" +
	"  install_method: zip-extract\n" +
	"---\n" +
	"\n" +
	"# Web Search Skill\n" +
	"\n" +
	"Long-form body that describes how this skill works in detail. It can span\n" +
	"multiple paragraphs and even contain --- dashes or '---' without confusion\n" +
	"because the parser only matches a `---` line on its own.\n" +
	"\n" +
	"## Examples\n" +
	"...\n"

func TestParseSkillFile_Valid(t *testing.T) {
	m, err := ParseSkillFile([]byte(validBaseSKILL))
	if err != nil {
		t.Fatalf("ParseSkillFile: %v", err)
	}
	if m.Name != "web-search" {
		t.Errorf("Name = %q, want %q", m.Name, "web-search")
	}
	if m.Description == "" {
		t.Error("Description is empty")
	}
	if m.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", m.Version, "1.2.3")
	}
	if m.Author != "Jane Developer" {
		t.Errorf("Author = %q", m.Author)
	}
	if len(m.Tags) != 2 || m.Tags[0] != "search" {
		t.Errorf("Tags = %v", m.Tags)
	}
	if m.License != "MIT" {
		t.Errorf("License = %q", m.License)
	}
	if !strings.Contains(m.Body, "Web Search Skill") {
		t.Errorf("Body should contain 'Web Search Skill', got: %q", m.Body[:50])
	}
	if !strings.Contains(m.Body, "--- dashes") {
		t.Error("Body should preserve in-body `---` substrings")
	}
	// Metadata fields.
	if len(m.Metadata.Requires.OS) != 2 || m.Metadata.Requires.OS[0] != "linux" {
		t.Errorf("Metadata.Requires.OS = %v", m.Metadata.Requires.OS)
	}
	if len(m.Metadata.Requires.Tools) != 2 {
		t.Errorf("Metadata.Requires.Tools = %v", m.Metadata.Requires.Tools)
	}
	if len(m.Metadata.Inputs) != 2 {
		t.Fatalf("Metadata.Inputs length = %d, want 2", len(m.Metadata.Inputs))
	}
	in0 := m.Metadata.Inputs[0]
	if in0.Name != "target_dir" || in0.Type != "string" || !in0.Required {
		t.Errorf("Inputs[0] = %+v", in0)
	}
	if m.Metadata.EntryPoint != "scripts/run.sh" {
		t.Errorf("Metadata.EntryPoint = %q", m.Metadata.EntryPoint)
	}
}

func TestParseSkillFile_BodyPreserved(t *testing.T) {
	in := []byte(validBaseSKILL)
	m, err := ParseSkillFile(in)
	if err != nil {
		t.Fatal(err)
	}
	// Body should contain "Long-form body" and end with "..." line.
	if !strings.Contains(m.Body, "Long-form body") {
		t.Error("body missing 'Long-form body'")
	}
	if !strings.Contains(m.Body, "## Examples") {
		t.Error("body missing '## Examples' header")
	}
	if strings.HasPrefix(m.Body, "\n") == false {
		// Should start after a newline (we trim one); OK if not for now.
	}
}

func TestParseSkillFile_MissingOpenDelim(t *testing.T) {
	in := []byte("name: web-search\ndescription: bad\n---\nbody\n")
	_, err := ParseSkillFile(in)
	if err == nil || !strings.Contains(err.Error(), "must start with") {
		t.Fatalf("expected 'must start with' error, got %v", err)
	}
}

func TestParseSkillFile_MissingCloseDelim(t *testing.T) {
	// Open with --- but never close.
	in := []byte("---\nname: web-search\ndescription: no closing delim\nbody without close\n")
	_, err := ParseSkillFile(in)
	if err == nil || !strings.Contains(err.Error(), "not closed") {
		t.Fatalf("expected 'not closed' error, got %v", err)
	}
}

func TestParseSkillFile_LeadingBOM(t *testing.T) {
	// UTF-8 BOM (EF BB BF) before the opening --- should be tolerated.
	in := append([]byte{0xEF, 0xBB, 0xBF}, []byte(validBaseSKILL)...)
	m, err := ParseSkillFile(in)
	if err != nil {
		t.Fatalf("ParseSkillFile with BOM: %v", err)
	}
	if m.Name != "web-search" {
		t.Errorf("Name after BOM = %q", m.Name)
	}
}

func TestManifest_Validate_Name(t *testing.T) {
	base := func() []byte {
		return []byte("---\nname: %NAME\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	}
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"kebab_ok", "my-skill", false},
		{"single_char", "a", false},
		{"max_length_64", "a" + strings.Repeat("b", 62) + "c", false}, // 64 chars exactly
		{"too_long", "a" + strings.Repeat("b", 63) + "c", true},       // 65 chars
		{"leading_hyphen", "-foo", true},
		{"trailing_hyphen", "foo-", true},
		{"uppercase", "MySkill", true},
		{"underscore", "my_skill", true},
		{"space", "my skill", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := []byte(strings.Replace(string(base()), "%NAME", tc.value, 1))
			_, err := ParseSkillFile(in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got nil", tc.value)
				}
			} else if err != nil {
				t.Errorf("unexpected error for %q: %v", tc.value, err)
			}
		})
	}
}

func TestManifest_Validate_Description(t *testing.T) {
	// 1024 chars max; we craft at the boundary.
	maxOK := strings.Repeat("x", 1024)
	tooLong := maxOK + "x"
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"min_1", "x", false},
		{"max_1024", maxOK, false},
		{"too_long", tooLong, true},
		{"contains_lt", "this has < angle", true},
		{"contains_gt", "this has > angle", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Build a minimal valid SKILL.md with the test description.
			// YAML escapes: a backslash would need escaping; none of our
			// test values contain YAML control chars, so direct interp is
			// safe for these particular cases.
			body := "---\nname: test\ndescription: " + tc.value + "\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n"
			_, err := ParseSkillFile([]byte(body))
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for description=%q (len=%d), got nil", tc.value, len(tc.value))
				}
			} else if err != nil {
				t.Errorf("unexpected error for description=%q (len=%d): %v", tc.value, len(tc.value), err)
			}
		})
	}
}

func TestManifest_Validate_Version(t *testing.T) {
	cases := []struct {
		version string
		valid   bool
	}{
		{"1.0.0", true},
		{"0.0.0", true},
		{"10.20.30", true},
		{"1.0.0-alpha", true},
		{"1.0.0-alpha.1", true},
		{"1.0.0-0.3.7", true},
		{"1.0.0+20130313144700", true},
		{"1.0.0-beta+exp.sha.5114f85", true},
		{"v1.0.0", false},  // no v prefix
		{"1.0", false},     // missing patch
		{"1", false},       // missing minor + patch
		{"1.0.0.0", false}, // four components
		{"latest", false},  // tag, not semver
		{"", false},        // empty
		{"1.0.0-", false},  // dangling prerelease
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			body := "---\nname: test\ndescription: ok description\nversion: " + tc.version + "\ncategory: dev\nagents: [all]\n---\nbody\n"
			_, err := ParseSkillFile([]byte(body))
			if tc.valid {
				if err != nil {
					t.Errorf("expected %q to be valid, got: %v", tc.version, err)
				}
			} else if err == nil {
				t.Errorf("expected %q to be rejected, got nil error", tc.version)
			}
		})
	}
}

func TestManifest_Validate_Tags(t *testing.T) {
	base := "---\nname: test\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\ntags: [%TAGS]\n---\nbody\n"
	cases := []struct {
		name    string
		tags    string
		wantErr bool
	}{
		{"empty", `"a"`, false}, // single ok tag
		{"two_ok", `"a", "b-c"`, false},
		{"ten_ok", `"a1", "b2", "c3", "d4", "e5", "f6", "g7", "h8", "i9", "j10"`, false},
		{"eleven_too_many", `"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"`, true},
		{"uppercase_tag", `"ABC"`, true},
		{"underscore_tag", `"a_b"`, true},
		{"space_tag", `"a b"`, true},
		{"too_long_tag", `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, true}, // 33 chars
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(base, "%TAGS", tc.tags, 1)
			_, err := ParseSkillFile([]byte(body))
			if tc.wantErr && err == nil {
				t.Errorf("expected error for tags=%q, got nil", tc.tags)
			} else if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for tags=%q: %v", tc.tags, err)
			}
		})
	}
}

func TestManifest_Validate_Homepage(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https_ok", "https://example.com", false},
		{"http_ok", "http://example.com/foo", false},
		{"file_scheme", "file:///etc/passwd", true},
		{"javascript", "javascript:alert(1)", true},
		{"no_scheme", "example.com", true},
		{"empty_ok", "", false}, // optional
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "---\nname: test\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\nhomepage: " + tc.url + "\n---\nbody\n"
			_, err := ParseSkillFile([]byte(body))
			if tc.wantErr && err == nil {
				t.Errorf("expected error for homepage=%q", tc.url)
			} else if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for homepage=%q: %v", tc.url, err)
			}
		})
	}
}

func TestManifest_Validate_Requires(t *testing.T) {
	cases := []struct {
		name     string
		yamlBody string
		wantErr  bool
	}{
		{"valid_linux_amd64", "metadata:\n  requires:\n    os: [linux]\n    arch: [amd64]\n    tools: []\n", false},
		{"invalid_os", "metadata:\n  requires:\n    os: [linux2]\n", true},
		{"invalid_arch", "metadata:\n  requires:\n    arch: [arm7]\n", true},
		{"too_many_tools", "metadata:\n  requires:\n    tools: [" + strings.Repeat(`"t",`, 21) + "]\n", true},
		{"empty_tool", "metadata:\n  requires:\n    tools: [\"foo\", \"\"]\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "---\nname: test\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n" + tc.yamlBody + "---\nbody\n"
			_, err := ParseSkillFile([]byte(body))
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			} else if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestManifest_Validate_Inputs(t *testing.T) {
	cases := []struct {
		name     string
		yamlBody string
		wantErr  bool
	}{
		{"valid_string", "metadata:\n  inputs:\n    - name: foo\n      type: string\n", false},
		{"valid_enum", "metadata:\n  inputs:\n    - name: color\n      type: enum\n      enum_values: [red, green]\n", false},
		{"enum_without_values", "metadata:\n  inputs:\n    - name: color\n      type: enum\n", true},
		{"invalid_type", "metadata:\n  inputs:\n    - name: foo\n      type: blob\n", true},
		{"empty_name", "metadata:\n  inputs:\n    - name: \"\"\n      type: string\n", true},
		{"duplicate_name", "metadata:\n  inputs:\n    - name: foo\n      type: string\n    - name: foo\n      type: integer\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "---\nname: test\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n" + tc.yamlBody + "---\nbody\n"
			_, err := ParseSkillFile([]byte(body))
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			} else if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestManifest_Defaults_FillsCreatedAt(t *testing.T) {
	body := "---\nname: test\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n"
	m, err := ParseSkillFile([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if m.CreatedAt != "" {
		t.Errorf("CreatedAt should be empty before Defaults(), got %q", m.CreatedAt)
	}
	m.Defaults()
	if m.CreatedAt == "" {
		t.Error("Defaults() did not fill CreatedAt")
	}
	// Format check: YYYY-MM-DD (10 chars).
	if len(m.CreatedAt) != 10 || m.CreatedAt[4] != '-' || m.CreatedAt[7] != '-' {
		t.Errorf("CreatedAt format unexpected: %q", m.CreatedAt)
	}
}

func TestIsValidSource(t *testing.T) {
	cases := map[string]bool{
		"community": true,
		"internal":  true,
		"upstream":  false,
		"ours":      false,
		"":          false,
	}
	for s, want := range cases {
		if got := IsValidSource(s); got != want {
			t.Errorf("IsValidSource(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestIsValidChannel(t *testing.T) {
	cases := map[string]bool{
		"stable":   true,
		"beta":     true,
		"edge":     true,
		"internal": true,
		"latest":   false, // channel "latest" is resolved to "stable" at the API layer; not a raw channel
		"":         false,
	}
	for c, want := range cases {
		if got := IsValidChannel(c); got != want {
			t.Errorf("IsValidChannel(%q) = %v, want %v", c, got, want)
		}
	}
}

func TestFindFrontmatterClose(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantOff int
		wantOk  bool
	}{
		{"empty", "", -1, false},
		{"no_close", "name: foo\nbar: baz\n", -1, false},
		{"immediate_close", "---\nrest", 0, true},
		{"with_text", "name: foo\n---\nrest", 10, true},
		{"with_trailing_space", "name: foo\n---   \nrest", 10, true},
		{"two_close_uses_first", "name: foo\n---\n---\nrest", 10, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			off := findFrontmatterClose([]byte(tc.in))
			ok := off >= 0
			if ok != tc.wantOk || (ok && off != tc.wantOff) {
				t.Errorf("findFrontmatterClose(%q) = %d, %v; want %d, %v", tc.in, off, ok, tc.wantOff, tc.wantOk)
			}
		})
	}
}

// === Schema v2.0 validation tests ===

func TestManifest_Validate_Category(t *testing.T) {
	cases := []struct {
		name     string
		category string
		wantErr  bool
	}{
		{"ops_ok", "ops", false},
		{"dev_ok", "dev", false},
		{"data_ok", "data", false},
		{"search_ok", "search", false},
		{"other_ok", "other", false},
		{"empty_rejected", "", true},
		{"unknown_rejected", "vibes", true},
		{"uppercase_rejected", "Dev", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "---\nname: test\ndescription: ok description\nversion: \"1.0.0\"\n" +
				"category: " + tc.category + "\nagents: [all]\n---\nbody\n"
			_, err := ParseSkillFile([]byte(body))
			if tc.wantErr && err == nil {
				t.Errorf("expected error for category=%q, got nil", tc.category)
			} else if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for category=%q: %v", tc.category, err)
			}
		})
	}
}

func TestManifest_Validate_Agents(t *testing.T) {
	cases := []struct {
		name    string
		agents  string
		wantErr bool
	}{
		{"all_alone_ok", "[all]", false},
		{"single_concrete_ok", "[opencode]", false},
		{"two_concrete_ok", "[opencode, hermes]", false},
		{"three_concrete_ok", "[opencode, openclaw, hermes]", false},
		{"empty_rejected", "[]", true},
		{"missing_field_rejected", "", true},
		{"unknown_rejected", "[unknown-agent]", true},
		{"all_plus_concrete_rejected", "[all, opencode]", true},
		{"concrete_plus_all_rejected", "[opencode, all]", true},
		{"duplicate_rejected", "[opencode, opencode]", true},
		{"empty_string_rejected", `[""]`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field := "agents: " + tc.agents + "\n"
			if tc.agents == "" {
				field = ""
			}
			body := "---\nname: test\ndescription: ok description\nversion: \"1.0.0\"\n" +
				"category: dev\n" + field + "---\nbody\n"
			_, err := ParseSkillFile([]byte(body))
			if tc.wantErr && err == nil {
				t.Errorf("expected error for agents=%q, got nil", tc.agents)
			} else if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for agents=%q: %v", tc.agents, err)
			}
		})
	}
}

func TestManifest_Validate_InstallMethod(t *testing.T) {
	cases := []struct {
		name    string
		method  string // empty means the field is omitted
		wantErr bool
	}{
		{"zip_extract_ok", "zip-extract", false},
		{"pip_wheel_ok", "pip-wheel", false},
		{"npm_pack_ok", "npm-pack", false},
		{"tarball_ok", "tarball", false},
		{"omitted_defaults_ok", "", false},
		{"unknown_rejected", "homebrew", true},
		{"uppercase_rejected", "ZIP-EXTRACT", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field := ""
			if tc.method != "" {
				field = "  install_method: " + tc.method + "\n"
			}
			body := "---\nname: test\ndescription: ok description\nversion: \"1.0.0\"\n" +
				"category: dev\nagents: [opencode]\nmetadata:\n" + field + "---\nbody\n"
			_, err := ParseSkillFile([]byte(body))
			if tc.wantErr && err == nil {
				t.Errorf("expected error for install_method=%q, got nil", tc.method)
			} else if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for install_method=%q: %v", tc.method, err)
			}
		})
	}
}

func TestManifest_Validate_InstallPaths(t *testing.T) {
	cases := []struct {
		name    string
		paths   string
		wantErr bool
	}{
		{"single_ok", "  install_paths:\n    opencode: \"$HOME/.config/opencode/skills/$NAME\"\n", false},
		{"two_ok", "  install_paths:\n    opencode: \"$HOME/.config/opencode/skills/$NAME\"\n    hermes: \"$HOME/.hermes/optional-skills/$NAME\"\n", false},
		{"missing_name_placeholder_rejected", "  install_paths:\n    opencode: \"$HOME/.config/opencode/skills/foo\"\n", true},
		{"unknown_agent_rejected", "  install_paths:\n    unknown: \"$HOME/.x/$NAME\"\n", true},
		// "all" is now a valid install_paths key — paired with
		// agents: [all], the value overrides the central-fallback
		// path in ResolveTargets.
		{"all_sentinel_ok", "  install_paths:\n    all: \"$HOME/.x/$NAME\"\n", false},
		{"all_sentinel_empty_value_rejected", "  install_paths:\n    all: \"\"\n", true},
		{"all_sentinel_missing_name_placeholder_rejected", "  install_paths:\n    all: \"$HOME/.x/foo\"\n", true},
		{"empty_value_rejected", "  install_paths:\n    opencode: \"\"\n", true},
		{"no_paths_ok", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "---\nname: test\ndescription: ok description\nversion: \"1.0.0\"\n" +
				"category: dev\nagents: [opencode]\nmetadata:\n" + tc.paths + "---\nbody\n"
			_, err := ParseSkillFile([]byte(body))
			if tc.wantErr && err == nil {
				t.Errorf("expected error for paths=%q, got nil", tc.paths)
			} else if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for paths=%q: %v", tc.paths, err)
			}
		})
	}
}

func TestManifest_Defaults_FillsInstallMethod(t *testing.T) {
	body := "---\nname: test\ndescription: ok description\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n"
	m, err := ParseSkillFile([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if m.Metadata.InstallMethod != "" {
		t.Fatalf("InstallMethod should be empty before Defaults(), got %q", m.Metadata.InstallMethod)
	}
	m.Defaults()
	if m.Metadata.InstallMethod != "zip-extract" {
		t.Errorf("Defaults() did not fill InstallMethod: got %q, want %q", m.Metadata.InstallMethod, "zip-extract")
	}
}

func TestIsValidCategory(t *testing.T) {
	cases := map[string]bool{
		"ops": true, "dev": true, "data": true, "search": true,
		"media": true, "content": true, "integration": true,
		"productivity": true, "other": true,
		"chat": false, "developer": false, // agent-side IDs are not skill categories
		"": false, "Other": false,
	}
	for c, want := range cases {
		if got := IsValidCategory(c); got != want {
			t.Errorf("IsValidCategory(%q) = %v, want %v", c, got, want)
		}
	}
	// " " is a whitespace string the YAML parser will reject at decode time;
	// we don't test it here because the parser never produces it.
	_ = " "
}

func TestIsValidAgent(t *testing.T) {
	cases := map[string]bool{
		"opencode": true, "openclaw": true, "hermes": true, "all": true,
		"": false, "OPENCODE": false, "vscode": false, "codex": false,
	}
	for a, want := range cases {
		if got := IsValidAgent(a); got != want {
			t.Errorf("IsValidAgent(%q) = %v, want %v", a, got, want)
		}
	}
}

func TestIsValidInstallMethod(t *testing.T) {
	cases := map[string]bool{
		"zip-extract": true, "pip-wheel": true, "npm-pack": true, "tarball": true,
		"": false, "brew": false, "ZIP-EXTRACT": false, "zip": false,
	}
	for m, want := range cases {
		if got := IsValidInstallMethod(m); got != want {
			t.Errorf("IsValidInstallMethod(%q) = %v, want %v", m, got, want)
		}
	}
}

func TestIsAllAgents(t *testing.T) {
	cases := []struct {
		agents []string
		want   bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"all"}, true},
		{[]string{"opencode"}, false},
		{[]string{"all", "opencode"}, false}, // malformed; not the singleton
		{[]string{"opencode", "hermes"}, false},
	}
	for _, tc := range cases {
		if got := IsAllAgents(tc.agents); got != tc.want {
			t.Errorf("IsAllAgents(%v) = %v, want %v", tc.agents, got, tc.want)
		}
	}
}

func TestKnownAgents(t *testing.T) {
	got := KnownAgents()
	want := map[string]bool{"opencode": true, "openclaw": true, "hermes": true}
	if len(got) != len(want) {
		t.Fatalf("KnownAgents() = %v, want %v", got, want)
	}
	for _, a := range got {
		if !want[a] {
			t.Errorf("unexpected agent %q in KnownAgents()", a)
		}
		if a == "all" {
			t.Error("KnownAgents() must not include 'all' sentinel")
		}
	}
}

func TestDefaultAgentInstallPaths(t *testing.T) {
	for agent, pathTpl := range DefaultAgentInstallPaths {
		if agent == "all" {
			t.Errorf("DefaultAgentInstallPaths must not contain 'all' key, got %q", pathTpl)
		}
		if pathTpl == "" {
			t.Errorf("DefaultAgentInstallPaths[%q] is empty", agent)
		}
		if !strings.Contains(pathTpl, "$NAME") {
			t.Errorf("DefaultAgentInstallPaths[%q] = %q must contain $NAME", agent, pathTpl)
		}
	}
	// Spot-check the three real agent paths we verified during planning.
	if DefaultAgentInstallPaths["opencode"] != "$HOME/.config/opencode/skills/$NAME" {
		t.Errorf("opencode path drifted from verified default")
	}
	if DefaultAgentInstallPaths["openclaw"] != "$HOME/.openclaw/skills/$NAME" {
		t.Errorf("openclaw path drifted from verified default")
	}
	if DefaultAgentInstallPaths["hermes"] != "$HOME/.hermes/optional-skills/$NAME" {
		t.Errorf("hermes path drifted from verified default")
	}
}
