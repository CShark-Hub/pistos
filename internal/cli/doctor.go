package cli

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/config"
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
	if len(lock.Packs) == 0 {
		fmt.Fprintln(a.stdout, "lock     no tools locked; run `pistos plugins install`")
	}
	return nil
}
