package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/plugin"
	"example.com/pistos/internal/store"
)

func (a *app) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report configuration, active packs, locked tools and their availability",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "pistos   %s (%s/%s)\nrepo     %s\n", Version, runtime.GOOS, runtime.GOARCH, repo)
			cfg, err := config.Load(repo)
			if err != nil {
				return withCode(ExitError, err)
			}
			fmt.Fprintf(a.stdout, "config   ok (schema %d, profile %s, execution %s, jobs %d)\n", cfg.Schema, cfg.Profile, cfg.Execution.Mode, cfg.Execution.Jobs)
			fmt.Fprintf(a.stdout, "packs    %s\n", strings.Join(cfg.ActiveLanguages(), ", "))
			for _, p := range cfg.Projects {
				fmt.Fprintf(a.stdout, "project  %-24s %-7s root=%s checks=%s\n", p.ID, p.Ecosystem, p.Root, strings.Join(p.Checks, ","))
			}
			lock, err := config.LoadLock(repo)
			if err != nil {
				return withCode(ExitError, err)
			}
			return a.doctorTools(repo, cfg, lock)
		},
	}
}

func (a *app) doctorTools(repo string, cfg *config.Config, lock *config.Lock) error {
	tb := plugin.NewToolbox(repo, store.Store{Repo: repo}.StateDir(), lock, plugin.BaseEnv(nil))
	defer tb.Save()
	problems := 0
	seen := map[string]bool{}
	for _, p := range cfg.Projects {
		eco, _ := config.NormalizeLanguage(p.Ecosystem)
		m, err := plugin.LoadManifest(eco)
		if err != nil {
			return withCode(ExitError, err)
		}
		for _, check := range p.Checks {
			if _, ok := m.Checks[check]; !ok {
				fmt.Fprintf(a.stdout, "✗ %s: unknown check %q\n", p.ID, check)
				problems++
				continue
			}
			for _, tool := range m.ToolsFor(check) {
				dir := filepath.Join(repo, filepath.FromSlash(p.Root))
				if seen[eco+tool+dir] {
					continue
				}
				seen[eco+tool+dir] = true
				rt, err := tb.Resolve(context.Background(), m, tool, dir)
				if err != nil {
					fmt.Fprintf(a.stdout, "✗ %-8s %-14s %v\n", eco, tool, err)
					problems++
					continue
				}
				fmt.Fprintf(a.stdout, "✓ %-8s %-14s %-12s %-8s %s\n", eco, tool, rt.Version, rt.Source, rt.Path)
			}
		}
	}
	if problems > 0 {
		return withCode(ExitError, fmt.Errorf("%d problem(s) found", problems))
	}
	return nil
}
