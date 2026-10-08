// Package report renders review envelopes for terminals and machines.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"example.com/pistos/internal/finding"
)

type TextOptions struct {
	IncludeIgnored  bool
	IncludeExisting bool
}

// Text renders the human summary: scope, coverage, issues and verdict.
func Text(w io.Writer, env *finding.Envelope, o TextOptions) {
	r := env.Canonical
	s := r.Scope
	switch s.Mode {
	case "all":
		fmt.Fprintf(w, "scope    full review of %s (tree %s)\n", s.HeadRef, short(s.HeadTree))
	default:
		base := s.BaseRef
		if s.BaseCommit != "" {
			base += " @ " + short(s.BaseCommit)
		}
		fmt.Fprintf(w, "scope    %s vs %s; %d changed file(s)\n", s.HeadRef, base, len(s.ChangedFiles))
	}
	if s.Empty {
		fmt.Fprintln(w, "         no changes: nothing was analyzed (this says nothing about the rest of the repository)")
	} else if len(s.Projects) == 0 {
		fmt.Fprintln(w, "         changes touch no configured project")
	} else {
		fmt.Fprintf(w, "projects %s\n", strings.Join(s.Projects, ", "))
	}

	if len(r.Coverage.Capabilities) > 0 {
		fmt.Fprintf(w, "checks   (profile %s)\n", r.Coverage.Profile)
		for _, c := range r.Coverage.Capabilities {
			req := ""
			if c.Required {
				req = " required"
			}
			line := fmt.Sprintf("  %-14s %-22s %-5s %s%s", c.Project, c.Capability, c.Side, c.Status, req)
			if c.ToolVersion != "" {
				line += " [" + c.Tool + " " + c.ToolVersion + "]"
			}
			fmt.Fprintln(w, line)
			if c.Error != "" {
				fmt.Fprintf(w, "      ! %s\n", c.Error)
			}
			for _, l := range c.Limitations {
				fmt.Fprintf(w, "      - %s\n", l)
			}
		}
	}
	fmt.Fprintf(w, "ai       %s\n", r.Coverage.AI)

	var shown, hidden, existing int
	for _, is := range r.Issues {
		if is.Change == "existing" && !o.IncludeExisting {
			existing++
			continue
		}
		if is.Disposition.State == finding.DispSuppressed && !o.IncludeIgnored {
			hidden++
			continue
		}
		if shown == 0 {
			fmt.Fprintln(w)
		}
		shown++
		printIssue(w, env.Numbers[is.Key], is)
	}
	fmt.Fprintln(w)
	if hidden > 0 {
		fmt.Fprintf(w, "%d suppressed by feedback (show with --include-ignored)\n", hidden)
	}
	if existing > 0 {
		fmt.Fprintf(w, "%d pre-existing issue(s) unchanged from the baseline are not listed\n", existing)
	}
	fmt.Fprintf(w, "verdict  %s (exit %d): %s\n", strings.ToUpper(r.Verdict.Outcome), r.Verdict.ExitCode, r.Verdict.Reason)
}

func printIssue(w io.Writer, n int, is finding.Issue) {
	mark := " "
	if is.Blocking {
		mark = "✗"
	}
	loc := is.Location.Path
	if is.Location.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, is.Location.Line)
		if is.Location.Column > 0 {
			loc += fmt.Sprintf(":%d", is.Location.Column)
		}
	}
	msg := is.Message
	if is.Metric != nil && is.Metric.Base != nil && *is.Metric.Base != is.Metric.Value {
		msg += fmt.Sprintf(" (was %d)", *is.Metric.Base)
	}
	if is.Advisory {
		mark = "·"
	}
	fmt.Fprintf(w, "%s #%-4d %-7s %s  %s\n", mark, n, is.Severity, loc, msg)
	var tools []string
	for _, s := range is.Sources {
		tools = append(tools, s.Tool+":"+s.Rule)
	}
	sort.Strings(tools)
	extra := ""
	if is.Change != "" && is.Change != "in-scope" {
		extra = " · " + is.Change
	}
	if is.Disposition.State != finding.DispActive {
		extra += " · " + is.Disposition.State
		if len(is.Disposition.Tags) > 0 {
			extra += " [" + strings.Join(is.Disposition.Tags, ",") + "]"
		}
	}
	fmt.Fprintf(w, "         %s · %s%s · key %s\n", is.Family, strings.Join(tools, " "), extra, is.Key)
	if is.AI != nil {
		fmt.Fprintf(w, "         ai: %s by %s (models %s)", is.AI.Outcome, strings.Join(is.AI.Passes, ","), strings.Join(is.AI.Models, ","))
		if is.AI.TriageReason != "" {
			fmt.Fprintf(w, " — %s", is.AI.TriageReason)
		}
		fmt.Fprintln(w)
		if is.AI.Carried != "" {
			fmt.Fprintf(w, "         %s\n", is.AI.Carried)
		}
	}
	if is.Disposition.Note != "" {
		fmt.Fprintf(w, "         note: %s\n", is.Disposition.Note)
	}
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// JSON writes the envelope: canonical report plus volatile telemetry and display numbers.
func JSON(w io.Writer, env *finding.Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}

// Canonical writes only the byte-comparable report.
func Canonical(w io.Writer, env *finding.Envelope) error {
	b, err := finding.CanonicalJSON(env.Canonical)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
