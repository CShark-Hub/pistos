package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/plugin"
	"example.com/pistos/internal/store"
)

func (a *app) pluginsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "plugins", Short: "Manage language packs and their tools"}
	var o plugin.InstallOptions
	install := &cobra.Command{
		Use:   "install",
		Short: "Locate or explicitly acquire the tools of active packs and record them in lock.json",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := a.repoRoot()
			if err != nil {
				return err
			}
			cfg, err := config.Load(repo)
			if err != nil {
				return withCode(ExitError, err)
			}
			lock, err := config.LoadLock(repo)
			if err != nil {
				return withCode(ExitError, err)
			}
			o.Repo, o.StateDir = repo, store.Store{Repo: repo}.StateDir()
			o.Log = func(s string) { fmt.Fprintln(a.stderr, "·", s) }
			results, ierr := plugin.Install(cmd.Context(), cfg, lock, o)
			for _, r := range results {
				if r.Err != nil {
					fmt.Fprintf(a.stdout, "✗ %-8s %-14s %v\n", r.Pack, r.Tool, r.Err)
					continue
				}
				fmt.Fprintf(a.stdout, "✓ %-8s %-14s %-12s %-8s %s (%s)\n", r.Pack, r.Tool, r.Version, r.Source, r.Path, r.Action)
			}
			if !o.Locked {
				if err := lock.Save(repo); err != nil {
					return withCode(ExitError, err)
				}
			}
			if !o.Locked && !o.Offline {
				a.offerAISetup(cmd.Context(), repo, cfg)
			}
			if ierr != nil {
				return withCode(ExitError, ierr)
			}
			return nil
		},
	}
	install.Flags().BoolVar(&o.Locked, "locked", false, "fail instead of changing lock.json")
	install.Flags().BoolVar(&o.LSP, "lsp", false, "also install and lock the language servers used by `pistos facts`")
	install.Flags().BoolVar(&o.Offline, "offline", false, "never download; only verify locally available tools")
	image := &cobra.Command{
		Use:   "image <pack>",
		Short: "Build the container image of a pack (go, python, jsts, csharp) from the sources embedded in pistos",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eco, err := config.NormalizeLanguage(args[0])
			if err != nil {
				return withCode(ExitError, err)
			}
			m, err := plugin.LoadManifest(eco)
			if err != nil {
				return withCode(ExitError, err)
			}
			rt, err := plugin.NewContainerRuntime()
			if err != nil {
				return withCode(ExitError, err)
			}
			id, err := rt.Build(cmd.Context(), m)
			if err != nil {
				return withCode(ExitError, err)
			}
			fmt.Fprintf(a.stdout, "%s %s\n", plugin.ImageTag(m), id)
			return nil
		},
	}
	cmd.AddCommand(install, image)
	return cmd
}
