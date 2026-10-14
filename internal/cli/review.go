package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/report"
	"example.com/pistos/internal/review"
	"example.com/pistos/internal/snapshot"
)

var reviewFormats = []string{"text", "agent", "json", "canonical-json", "sarif"}

// reviewOutput says how a finished review is rendered.
type reviewOutput struct {
	w         io.Writer
	format    string
	text      report.TextOptions
	maxIssues int
	// html writes .pistos/state/report.html; announce prints its link to stderr.
	html     bool
	announce bool
}

// executeReview runs a review and renders it. Errors carry their exit code; the verdict's exit code
// is left to the caller (git hooks map it differently).
func (a *app) executeReview(ctx context.Context, o review.Options, out reviewOutput) (*finding.Envelope, error) {
	// Verbose runs print progress lines instead, which a spinner would overwrite.
	var spin *spinner
	if o.Progress == nil {
		spin = startSpinner(a.stderr, "reviewing")
		o.Status = func(s string) { spin.Set("reviewing · " + s) }
	}
	env, err := review.Run(ctx, o)
	spin.Stop()
	if err != nil {
		var ce *review.ConfigError
		if errors.As(err, &ce) {
			return nil, withCode(ExitError, err)
		}
		if ctx.Err() != nil {
			return nil, withCode(ExitCancelled, nil)
		}
		return nil, withCode(ExitError, err)
	}
	htmlPath := ""
	if out.html {
		if p, err := writeHTMLReport(ctx, o.Repo, env); err != nil {
			fmt.Fprintln(a.stderr, "note: html report not written:", err)
		} else {
			htmlPath = p
		}
	}
	switch out.format {
	case "json":
		err = report.JSON(out.w, env)
	case "canonical-json":
		err = report.Canonical(out.w, env)
	case "sarif":
		err = report.SARIF(out.w, env, Version)
	case "agent":
		g := snapshot.Git{Repo: o.Repo}
		tree := env.Canonical.Scope.HeadTree
		rel := htmlPath
		if r, err := filepath.Rel(o.Repo, htmlPath); err == nil && htmlPath != "" && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
		report.Agent(out.w, env, report.AgentOptions{
			MaxIssues:  out.maxIssues,
			Source:     func(p string) ([]byte, error) { return g.Show(ctx, tree, p) },
			ReportPath: rel,
		})
	default:
		report.Text(out.w, env, out.text)
	}
	if err != nil {
		return nil, withCode(ExitError, err)
	}
	if htmlPath != "" && out.announce {
		a.announceReport(htmlPath)
	}
	return env, nil
}

// inheritedIndexFile returns the index git handed to a hook through GIT_INDEX_FILE, made absolute
// against the working directory git started the hook in ("" when unset).
func inheritedIndexFile() (string, error) {
	p := os.Getenv("GIT_INDEX_FILE")
	if p == "" {
		return "", nil
	}
	return filepath.Abs(p)
}

func (a *app) reviewCommand() *cobra.Command {
	var o review.Options
	var format string
	var includeIgnored, includeExisting, verbose, noHTML bool
	var maxIssues int
	var maxAIUSD float64
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review changes with the fixed deterministic check plan",
		Long: `Review the working snapshot (staged, unstaged and untracked files) against HEAD, or against
merge-base(--base, HEAD). With --head, review that commit against merge-base(--base, --head),
independently of uncommitted changes. --staged reviews only the index (what the next commit would
contain), against HEAD or merge-base(--base, HEAD). --all reviews the whole working snapshot.

--format agent prints a compact list of the issues the change introduced, for coding agents.`,
		Example: `  pistos review
  pistos review --base main
  pistos review --base main --head HEAD
  pistos review --staged
  pistos review --format agent
  pistos review --all --format json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			o.Repo = repo
			if cmd.Flags().Changed("max-ai-usd") {
				o.MaxAIUSD = &maxAIUSD
			}
			if verbose {
				o.Progress = func(s string) { fmt.Fprintln(a.stderr, "·", s) }
			}
			if !contains(reviewFormats, format) {
				return withCode(ExitError, fmt.Errorf("unsupported --format %q (%s)", format, strings.Join(reviewFormats, ", ")))
			}
			if o.Staged {
				o.IndexFile, err = inheritedIndexFile()
				if err != nil {
					return withCode(ExitError, err)
				}
			}
			env, err := a.executeReview(cmd.Context(), o, reviewOutput{
				w: a.stdout, format: format, maxIssues: maxIssues, html: !noHTML, announce: format != "agent",
				text: report.TextOptions{IncludeIgnored: includeIgnored, IncludeExisting: includeExisting || o.All},
			})
			if err != nil {
				return err
			}
			if code := env.Canonical.Verdict.ExitCode; code != 0 {
				return withCode(code, nil)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Base, "base", "", "compare against merge-base(BASE, head)")
	f.StringVar(&o.Head, "head", "", "review this commit instead of the working snapshot (requires --base)")
	f.BoolVar(&o.All, "all", false, "review every configured project in full")
	f.StringVar(&o.Profile, "profile", "", "fast, balanced or thorough (default from config)")
	f.BoolVar(&o.Offline, "offline", false, "forbid network access by pistos and declared tool fetches")
	f.IntVarP(&o.Jobs, "jobs", "j", 0, "parallel job budget (default from config)")
	f.BoolVar(&o.Staged, "staged", false, "review the index (staged changes only; honors GIT_INDEX_FILE in git hooks)")
	f.StringVar(&format, "format", "text", "text, agent, json, canonical-json or sarif")
	f.IntVar(&maxIssues, "max-issues", 20, "issues listed by --format agent before pointing to the full report")
	f.BoolVar(&includeIgnored, "include-ignored", false, "also list issues suppressed by feedback")
	f.BoolVar(&includeExisting, "include-existing", false, "also list issues already present in the baseline")
	f.BoolVar(&o.RefreshAI, "refresh-ai", false, "run fresh AI discovery instead of replaying recorded responses")
	f.BoolVar(&o.FeedbackFromBase, "feedback-from-base", false, "apply the baseline's committed decisions (for untrusted changes)")
	f.Float64Var(&maxAIUSD, "max-ai-usd", 0, "cap this review's estimated AI spend in USD, overriding ai.budget.max_review_usd (0 = unlimited)")
	f.BoolVar(&noHTML, "no-html", false, "do not write .pistos/state/report.html")
	f.BoolVarP(&verbose, "verbose", "v", false, "print progress to stderr")
	return cmd
}
