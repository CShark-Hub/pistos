// Package cli wires subcommands to the review, feedback and plugin services.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/detect"
)

const (
	ExitOK        = 0
	ExitBlocking  = 1
	ExitError     = 2
	ExitCancelled = 130
)

var Version = "0.1.0-dev"

// exitError carries a specific exit code through cobra's error return.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func withCode(code int, err error) error { return &exitError{code: code, err: err} }

type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	repo   string
}

func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return Run(ctx, args, stdin, stdout, stderr)
}

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr}
	root := a.rootCommand()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "pistos: cancelled")
		return ExitCancelled
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.err != nil {
			fmt.Fprintln(stderr, "pistos:", ee.err)
		}
		return ee.code
	}
	fmt.Fprintln(stderr, "pistos:", err)
	return ExitError
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "pistos",
		Short:         "Local-first code review with deterministic gates and optional BYOK AI",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&a.repo, "repo", "C", "", "repository root (default: enclosing git repository)")
	root.AddCommand(a.initCommand(), a.doctorCommand(), a.pluginsCommand(), a.reviewCommand(),
		a.tagCommand(), a.reopenCommand(), a.feedbackCommand(), a.reproduceCommand(), a.factsCommand(), a.reportCommand(), a.aiCommand())
	return root
}

// repoRoot resolves the enclosing repository without invoking git so init works anywhere.
func (a *app) repoRoot() (string, error) {
	start := a.repo
	if start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		start = wd
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if a.repo != "" {
		return abs, nil
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			return abs, nil
		}
	}
}

func (a *app) initCommand() *cobra.Command {
	var langs []string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Detect projects and write .pistos/ configuration (idempotent; never installs tools)",
		Example: `  pistos init
  pistos init --lang csharp
  pistos init --lang go --lang python
  pistos init --lang "c#"   # quote c# so the shell does not treat # as a comment`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			var selected []string
			for _, l := range langs {
				eco, err := config.NormalizeLanguage(l)
				if err != nil {
					return withCode(ExitError, err)
				}
				selected = append(selected, eco)
			}
			projects, err := detect.Projects(repo, selected)
			if err != nil {
				return err
			}
			active := selected
			if len(active) == 0 {
				seen := map[string]bool{}
				for _, p := range projects {
					if !seen[p.Ecosystem] {
						seen[p.Ecosystem] = true
						active = append(active, p.Ecosystem)
					}
				}
			}
			res, err := config.Init(repo, dedupe(active), projects)
			if err != nil {
				return withCode(ExitError, err)
			}
			for _, f := range res.Created {
				fmt.Fprintf(a.stdout, "created  %s\n", f)
			}
			for _, f := range res.Kept {
				fmt.Fprintf(a.stdout, "kept     %s (existing, unchanged)\n", f)
			}
			if len(res.Created) > 0 && contains(res.Created, ".pistos/config.toml") {
				if len(projects) == 0 {
					fmt.Fprintln(a.stdout, "no projects detected; add [[projects]] entries to .pistos/config.toml")
				}
				for _, p := range projects {
					fmt.Fprintf(a.stdout, "project  %-24s %-7s root=%s checks=%s\n", p.ID, p.Ecosystem, p.Root, strings.Join(p.Checks, ","))
				}
			}
			for _, p := range res.Suggested {
				fmt.Fprintf(a.stdout, "detected %s (%s at %s) is not in config.toml; add it manually if wanted\n", p.ID, p.Ecosystem, p.Root)
			}
			fmt.Fprintln(a.stdout, "next: pistos plugins install   # explicit tool acquisition and lock")
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&langs, "lang", nil, "ecosystem to enable (go, python, js, ts, csharp, \"c#\"); repeatable")
	return cmd
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
