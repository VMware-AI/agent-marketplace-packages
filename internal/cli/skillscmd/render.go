package skillscmd

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
)

// renderSkillsTable prints a human-readable table of skill packages.
// Mirrors renderAgentsTable in internal/cli/render.go but for skills.
func renderSkillsTable(w io.Writer, ss []skills.SkillStripped) {
	if len(ss) == 0 {
		fmt.Fprintln(w, "(no skills match the filter)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDISPLAY\tLATEST\tVERSIONS\tDESCRIPTION")
	fmt.Fprintln(tw, "----\t-------\t------\t--------\t-----------")
	for _, s := range ss {
		latest := latestSkillVersionLabel(s.Versions)
		versions := skillVersionCount(s.Versions)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			s.Name,
			truncate(s.DisplayName, 18),
			latest,
			versions,
			truncate(s.Description, 60),
		)
	}
	_ = tw.Flush()
}

// latestSkillVersionLabel returns "X.Y.Z (channel)" for the highest-version
// entry in the skill's version list, or "—" if empty. The caller is
// expected to have already filtered to a single channel (or the server
// has done so via ?channel=); this picks the highest semver among
// whatever survives.
func latestSkillVersionLabel(versions []skills.SkillVersionStripped) string {
	if len(versions) == 0 {
		return "—"
	}
	// Pick the first stable version (or first if no stable).
	var pick *skills.SkillVersionStripped
	for i := range versions {
		if versions[i].Channel == "stable" {
			pick = &versions[i]
			break
		}
	}
	if pick == nil {
		pick = &versions[0]
	}
	return fmt.Sprintf("%s (%s)", pick.Version, pick.Channel)
}

// skillVersionCount formats the version count for the table.
func skillVersionCount(versions []skills.SkillVersionStripped) string {
	if len(versions) == 1 {
		return "1"
	}
	return fmt.Sprintf("%d", len(versions))
}

// renderSkillDetails prints one skill's full metadata + per-version
// summary. Mirrors renderAgentDetails for agents.
func renderSkillDetails(w io.Writer, s *skills.Skill) {
	fmt.Fprintf(w, "%s  (%s)\n", s.DisplayName, s.Name)
	fmt.Fprintf(w, "  Author:      %s\n", s.Author)
	fmt.Fprintf(w, "  License:     %s\n", s.License)
	fmt.Fprintf(w, "  Description: %s\n", s.Description)
	if len(s.Tags) > 0 {
		fmt.Fprintf(w, "  Tags:        %v\n", s.Tags)
	} else {
		fmt.Fprintf(w, "  Tags:        (none)\n")
	}
	fmt.Fprintln(w, "  Versions:")
	for _, v := range s.Versions {
		fmt.Fprintf(w, "    - %s (%s/%s)  bundle=%s  size=%d  sha256=%s\n",
			v.Version, v.Source, v.Channel,
			v.Zip.Filename, v.Zip.SizeBytes, v.Zip.SHA256)
		if v.EntryPoint != "" {
			fmt.Fprintf(w, "      entry_point: %s\n", v.EntryPoint)
		}
	}
}

// renderSkillVersionDetails prints one SkillVersion in full.
func renderSkillVersionDetails(w io.Writer, sv *skills.SkillVersion) {
	fmt.Fprintf(w, "Skill:    %s\n", "see /skills/{source}/{name}")
	fmt.Fprintf(w, "Source:   %s\n", sv.Source)
	fmt.Fprintf(w, "Version:  %s\n", sv.Version)
	fmt.Fprintf(w, "Channel:  %s\n", sv.Channel)
	fmt.Fprintf(w, "Released: %s\n", sv.ReleasedAt)
	fmt.Fprintf(w, "Bundle:   %s  (%d bytes)\n", sv.Zip.Filename, sv.Zip.SizeBytes)
	fmt.Fprintf(w, "SHA256:   %s\n", sv.Zip.SHA256)
	if sv.EntryPoint != "" {
		fmt.Fprintf(w, "Entry:    %s\n", sv.EntryPoint)
	}
	if sv.Requires != nil && (len(sv.Requires.OS) > 0 || len(sv.Requires.Arch) > 0 || len(sv.Requires.Tools) > 0) {
		fmt.Fprintf(w, "Requires: os=%v arch=%v tools=%v\n",
			sv.Requires.OS, sv.Requires.Arch, sv.Requires.Tools)
	}
	if len(sv.Inputs) > 0 {
		fmt.Fprintln(w, "Inputs:")
		for _, in := range sv.Inputs {
			req := ""
			if in.Required {
				req = " (required)"
			}
			fmt.Fprintf(w, "  - %s : %s%s\n", in.Name, in.Type, req)
			if in.Description != "" {
				fmt.Fprintf(w, "    %s\n", in.Description)
			}
		}
	}
	if sv.Body != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "--- body ---")
		fmt.Fprintln(w, sv.Body)
	}
}

// truncate is shared with the agent renderers via cli.render.go in
// spirit, but we keep a local copy so skillscmd has zero cross-package
// rendering dependencies. Identical semantics: shorten to n chars + "…".
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}