package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/review"
)

func (a *app) factsCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "facts <definition|references|implementation|diagnostics> <path[:line:column]>",
		Short: "Ask the owning project's language server for semantic facts (started on demand)",
		Long: `Starts only the language server of the project that owns the file, inside the working snapshot,
and maps answers back to repository paths. Lines and columns are 1-based; columns count bytes.
Diagnostics are complete only when the server supports pull diagnostics; push-only servers give no
completion signal, so their answer is reported as incomplete.`,
		Example: `  pistos facts definition svc/main.go:12:9
  pistos facts references pkg/calc.py:1:5 --format json
  pistos facts diagnostics src/math.ts`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			q := review.FactsQuery{Repo: repo, Method: args[0], Line: 1, Column: 1}
			parts := strings.Split(args[1], ":")
			q.Path = parts[0]
			if len(parts) == 3 {
				if q.Line, err = strconv.Atoi(parts[1]); err == nil {
					q.Column, err = strconv.Atoi(parts[2])
				}
			} else if len(parts) != 1 {
				err = errors.New("expected path or path:line:column")
			}
			if err != nil {
				return withCode(ExitError, err)
			}
			res, err := review.Facts(cmd.Context(), q)
			if err != nil {
				return withCode(ExitError, err)
			}
			if format == "json" {
				enc := json.NewEncoder(a.stdout)
				enc.SetIndent("", "  ")
				_ = enc.Encode(res)
			} else {
				a.printFacts(res)
			}
			if !res.Status.Completed() {
				return withCode(ExitError, nil)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "text or json")
	return cmd
}

func (a *app) printFacts(r *review.FactsResult) {
	cached := ""
	if r.Cached {
		cached = " (cached)"
	}
	fmt.Fprintf(a.stdout, "%s %s:%d:%d via %s %s in %s — %s%s\n", r.Method, r.Path, r.Line, r.Column, r.Backend, r.Version, r.Project, r.Status, cached)
	if r.Reason != "" {
		fmt.Fprintf(a.stdout, "  note: %s\n", r.Reason)
	}
	for _, l := range r.Locations {
		ext := ""
		if l.External {
			ext = " (outside the repository)"
		}
		fmt.Fprintf(a.stdout, "  %s:%d:%d%s\n", l.Path, l.Line, l.Column, ext)
	}
	for _, d := range r.Diagnostics {
		fmt.Fprintf(a.stdout, "  %d:%d %s %s %s\n", d.Line, d.Column, d.Severity, d.Code, d.Message)
	}
	if r.Status == finding.StatusComplete && len(r.Locations) == 0 && len(r.Diagnostics) == 0 {
		fmt.Fprintln(a.stdout, "  (none)")
	}
}
