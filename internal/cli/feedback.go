package cli

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/feedback"
	"example.com/pistos/internal/review"
)

const feedbackHelp = `commands:
  list                 list decisions
  show N               show decision N
  issue N              keep decision N scoped to this issue only
  pattern N [GLOB...]  widen decision N to its rule/pattern (preview, then confirm)
  tag N TAG            add a tag
  untag N TAG          remove a tag
  reason N TEXT        set the optional explanation
  delete N             delete decision N
  quit`

func (a *app) feedbackCommand() *cobra.Command {
	var list bool
	cmd := &cobra.Command{
		Use:   "feedback",
		Short: "Inspect and edit review decisions interactively (or --list)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			f, err := feedback.Load(repo)
			if err != nil {
				return withCode(ExitError, err)
			}
			a.listDecisions(f)
			if list {
				return nil
			}
			fmt.Fprintln(a.stdout, feedbackHelp)
			in := bufio.NewScanner(a.stdin)
			for {
				fmt.Fprint(a.stdout, "feedback> ")
				if !in.Scan() {
					fmt.Fprintln(a.stdout)
					return nil
				}
				fields := strings.Fields(in.Text())
				if len(fields) == 0 {
					continue
				}
				if fields[0] == "quit" || fields[0] == "q" {
					return nil
				}
				if err := a.feedbackAction(cmd.Context(), repo, fields, in); err != nil {
					fmt.Fprintln(a.stdout, "error:", err)
				}
			}
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "print decisions and exit")
	return cmd
}

func (a *app) listDecisions(f *feedback.File) {
	if len(f.Decisions) == 0 {
		fmt.Fprintln(a.stdout, "no decisions recorded")
		return
	}
	for i, d := range f.Decisions {
		fmt.Fprintf(a.stdout, "%3d  %s  [%s]  %s\n", i+1, d.ID, strings.Join(d.Tags, ","), d.Describe())
	}
}

func (a *app) feedbackAction(ctx context.Context, repo string, fields []string, in *bufio.Scanner) error {
	if fields[0] == "list" {
		f, err := feedback.Load(repo)
		if err != nil {
			return err
		}
		a.listDecisions(f)
		return nil
	}
	if len(fields) < 2 {
		return fmt.Errorf("usage:\n%s", feedbackHelp)
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil || n < 1 {
		return fmt.Errorf("%q is not a decision number", fields[1])
	}
	rest := fields[2:]

	edit := func(fn func(d *feedback.Decision, f *feedback.File) error) error {
		_, err := feedback.Update(ctx, repo, func(f *feedback.File) error {
			if n > len(f.Decisions) {
				return fmt.Errorf("no decision %d", n)
			}
			return fn(&f.Decisions[n-1], f)
		})
		if err != nil {
			return err
		}
		if env, err := review.Refilter(ctx, repo); err == nil {
			a.printRefiltered(env)
		}
		return nil
	}

	switch fields[0] {
	case "show":
		f, err := feedback.Load(repo)
		if err != nil {
			return err
		}
		if n > len(f.Decisions) {
			return fmt.Errorf("no decision %d", n)
		}
		d := f.Decisions[n-1]
		fmt.Fprintf(a.stdout, "id       %s (version %d)\ntags     %s\nscope    %s\n", d.ID, d.Version, strings.Join(d.Tags, ", "), d.Describe())
		if d.Reason != "" {
			fmt.Fprintf(a.stdout, "reason   %s\n", d.Reason)
		}
		if d.Guards != nil && d.Guards.TestPath {
			fmt.Fprintln(a.stdout, "guard    path must remain test code")
		}
		return nil
	case "issue":
		return edit(func(d *feedback.Decision, _ *feedback.File) error {
			if d.Scope != feedback.ScopeIssue {
				return fmt.Errorf("a pattern decision cannot be narrowed back to an issue; delete it and tag the issue instead")
			}
			fmt.Fprintln(a.stdout, "already scoped to this issue only")
			return nil
		})
	case "pattern":
		f, err := feedback.Load(repo)
		if err != nil {
			return err
		}
		if n > len(f.Decisions) || f.Decisions[n-1].Scope != feedback.ScopeIssue {
			return fmt.Errorf("decision %d is not an issue decision", n)
		}
		src := f.Decisions[n-1]
		p := feedback.Pattern{Project: src.Match.Project, Family: src.Match.Family, Paths: rest}
		a.printPatternPreview(repo, p)
		fmt.Fprint(a.stdout, "apply this rule/pattern decision? [y/N] ")
		if !in.Scan() || strings.ToLower(strings.TrimSpace(in.Text())) != "y" {
			fmt.Fprintln(a.stdout, "kept as this issue only")
			return nil
		}
		return edit(func(d *feedback.Decision, f *feedback.File) error {
			f.Decisions = append(f.Decisions, feedback.Decision{
				ID:   feedback.NewID("pattern\x00" + p.Project + "\x00" + p.Family + "\x00" + strings.Join(p.Paths, "\x00")),
				Tags: d.Tags, Reason: d.Reason, Scope: feedback.ScopePattern, Pattern: &p, Version: 1,
			})
			return nil
		})
	case "tag", "untag":
		if len(rest) != 1 || !feedback.ValidTag(rest[0]) {
			return fmt.Errorf("usage: %s N TAG", fields[0])
		}
		return edit(func(d *feedback.Decision, _ *feedback.File) error {
			if fields[0] == "tag" {
				d.AddTags(rest[0])
			} else {
				d.RemoveTags(func(t string) bool { return t == rest[0] })
				if len(d.Tags) == 0 {
					return fmt.Errorf("a decision needs at least one tag; use delete instead")
				}
			}
			d.Version++
			return nil
		})
	case "reason":
		return edit(func(d *feedback.Decision, _ *feedback.File) error {
			d.Reason = strings.Join(rest, " ")
			d.Version++
			return nil
		})
	case "delete":
		fmt.Fprint(a.stdout, "delete this decision? [y/N] ")
		if !in.Scan() || strings.ToLower(strings.TrimSpace(in.Text())) != "y" {
			return nil
		}
		return edit(func(_ *feedback.Decision, f *feedback.File) error {
			f.Decisions = append(f.Decisions[:n-1], f.Decisions[n:]...)
			return nil
		})
	}
	return fmt.Errorf("unknown command %q\n%s", fields[0], feedbackHelp)
}
