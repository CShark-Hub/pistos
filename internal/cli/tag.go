package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/feedback"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/review"
)

func (a *app) tagCommand() *cobra.Command {
	var o review.TagOptions
	var scope string
	var yes bool
	cmd := &cobra.Command{
		Use:   "tag <issue> <tag>...",
		Short: "Record a decision for an issue in .pistos/feedback.yaml",
		Long: `Tag an issue by its number in the current report or by its canonical key.

Reserved tags: irrelevant, false-positive and accepted-risk suppress the issue from reports and the
gate; deferred keeps it visible and gating. Any other tag (test-only, security, …) is a label.
The decision applies to this issue only, follows line shifts, formatting and verified renames, and
survives cache deletion. --scope pattern widens it to every finding of the same family under --paths
and requires --yes after reviewing the printed preview.`,
		Example: `  pistos tag 47 irrelevant
  pistos tag 47 test-only accepted-risk --reason "fixture is intentionally vulnerable"
  pistos tag 47 irrelevant --scope pattern --paths "gen/**" --yes`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			o.Tags = args[1:]
			o.Notices = func(s string) { fmt.Fprintln(a.stderr, "note:", s) }
			switch scope {
			case feedback.ScopeIssue:
			case feedback.ScopePattern:
				o.Pattern = true
				r, err := review.ResolveRef(repo, args[0])
				if err != nil {
					return withCode(ExitError, err)
				}
				p := feedback.Pattern{Project: r.Issue.Project, Family: r.Issue.Family, Paths: o.PatternPaths}
				a.printPatternPreview(repo, p)
				if !yes {
					return withCode(ExitError, fmt.Errorf("pattern decisions widen the exception; re-run with --yes to confirm"))
				}
			default:
				return withCode(ExitError, fmt.Errorf("--scope must be issue or pattern"))
			}
			d, env, err := review.Tag(cmd.Context(), repo, args[0], o)
			if err != nil {
				return withCode(ExitError, err)
			}
			fmt.Fprintf(a.stdout, "%s %s → [%s] (%s, version %d)\n", args[0], d.ID, strings.Join(d.Tags, ", "), d.Describe(), d.Version)
			a.printRefiltered(env)
			a.refreshHTMLReport(cmd.Context(), repo, env)
			return nil
		},
	}
	cmd.Flags().StringVar(&o.Reason, "reason", "", "optional explanation stored with the decision")
	cmd.Flags().StringVar(&scope, "scope", feedback.ScopeIssue, "issue or pattern")
	cmd.Flags().StringSliceVar(&o.PatternPaths, "paths", nil, "path globs for --scope pattern (default: all paths in the project)")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm a pattern decision after its preview")
	return cmd
}

func (a *app) reopenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <issue>",
		Short: "Remove the suppressing/deferral tags applied to an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			env, err := review.Reopen(cmd.Context(), repo, args[0])
			if err != nil {
				return withCode(ExitError, err)
			}
			fmt.Fprintf(a.stdout, "%s reopened\n", args[0])
			a.printRefiltered(env)
			a.refreshHTMLReport(cmd.Context(), repo, env)
			return nil
		},
	}
}

func (a *app) printRefiltered(env *finding.Envelope) {
	if env == nil {
		return
	}
	v := env.Canonical.Verdict
	fmt.Fprintf(a.stdout, "current report refiltered: %s (exit %d) — %s\n", strings.ToUpper(v.Outcome), v.ExitCode, v.Reason)
}

func (a *app) printPatternPreview(repo string, p feedback.Pattern) {
	issues, numbers, err := review.PatternPreview(repo, p)
	scope := "all paths"
	if len(p.Paths) > 0 {
		scope = strings.Join(p.Paths, ", ")
	}
	fmt.Fprintf(a.stdout, "pattern preview: every %s finding in project %s under %s\n", p.Family, p.Project, scope)
	if err != nil {
		fmt.Fprintf(a.stdout, "  (no current report to preview: %v)\n", err)
		return
	}
	fmt.Fprintf(a.stdout, "  currently matches %d issue(s):\n", len(issues))
	for _, is := range issues {
		fmt.Fprintf(a.stdout, "  #%-4d %s:%d %s\n", numbers[is.Key], is.Location.Path, is.Location.Line, is.Message)
	}
	fmt.Fprintln(a.stdout, "  future findings of this family under these paths will match too")
}

func (a *app) reproduceCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "reproduce <issue>",
		Short: "Show an issue's evidence, provenance and the command that reproduces it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			r, err := review.ResolveRef(repo, args[0])
			if err != nil {
				return withCode(ExitError, err)
			}
			is := r.Issue
			fmt.Fprintf(a.stdout, "issue    #%d %s\nfamily   %s (%s lane)\nwhere    %s:%d:%d", r.Number, is.Key, is.Family, is.Lane, is.Location.Path, is.Location.Line, is.Location.Column)
			if is.Anchor.Symbol != "" {
				fmt.Fprintf(a.stdout, " in %s", is.Anchor.Symbol)
			}
			fmt.Fprintf(a.stdout, "\nproject  %s\ntree     %s\nevidence %s\n", is.Project, r.Tree, is.EvidenceHash)
			for _, s := range is.Sources {
				fmt.Fprintf(a.stdout, "source   %s %s [%s]: %s\n", s.Tool, s.Rule, s.Severity, s.Message)
			}
			for _, c := range is.Reproduce {
				fmt.Fprintf(a.stdout, "run      %s\n", c)
			}
			if is.Disposition.Decision != "" {
				fmt.Fprintf(a.stdout, "decision %s (%s) %s\n", is.Disposition.Decision, is.Disposition.State, is.Disposition.Note)
			}
			return nil
		},
	}
}
