package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/report"
	"example.com/pistos/internal/review"
)

func (a *app) reviewCommand() *cobra.Command {
	var o review.Options
	var format string
	var includeIgnored, includeExisting, verbose bool
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review changes with the fixed deterministic check plan",
		Long: `Review the working snapshot (staged, unstaged and untracked files) against HEAD, or against
merge-base(--base, HEAD). With --head, review that commit against merge-base(--base, --head),
independently of uncommitted changes. --all reviews the whole working snapshot.`,
		Example: `  pistos review
  pistos review --base main
  pistos review --base main --head HEAD
  pistos review --all --format json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			o.Repo = repo
			if verbose {
				o.Progress = func(s string) { fmt.Fprintln(a.stderr, "·", s) }
			}
			if format != "text" && format != "json" && format != "canonical-json" {
				return withCode(ExitError, fmt.Errorf("unsupported --format %q (text, json, canonical-json)", format))
			}
			env, err := review.Run(cmd.Context(), o)
			if err != nil {
				var ce *review.ConfigError
				if errors.As(err, &ce) {
					return withCode(ExitError, err)
				}
				if cmd.Context().Err() != nil {
					return withCode(ExitCancelled, nil)
				}
				return withCode(ExitError, err)
			}
			switch format {
			case "json":
				err = report.JSON(a.stdout, env)
			case "canonical-json":
				err = report.Canonical(a.stdout, env)
			default:
				report.Text(a.stdout, env, report.TextOptions{IncludeIgnored: includeIgnored, IncludeExisting: includeExisting || o.All})
			}
			if err != nil {
				return withCode(ExitError, err)
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
	f.StringVar(&format, "format", "text", "text, json or canonical-json")
	f.BoolVar(&includeIgnored, "include-ignored", false, "also list issues suppressed by feedback")
	f.BoolVar(&includeExisting, "include-existing", false, "also list issues already present in the baseline")
	f.BoolVarP(&verbose, "verbose", "v", false, "print progress to stderr")
	return cmd
}
