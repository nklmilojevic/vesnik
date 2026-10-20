package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nklmilojevic/vesnik/internal/ai"
	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/gitx"
	"github.com/nklmilojevic/vesnik/internal/setup"
)

func newInitCmd(g *globals) *cobra.Command {
	var (
		force bool
		noAI  bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter .vesnik.yaml, with a model reading the repository",
		Long: `Write a starter .vesnik.yaml. vesnik detects packages and ecosystems itself,
then a model reads the README, manifests, release recipes, workflow triggers,
recent commits and version-looking lines, and suggests the product line,
style rules, release branch, changelog and version files. Each suggestion is
checked against the repository before it is written. Only tracked files are
read. Pass --no-ai to skip the model.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			repo, err := gitx.Open(ctx, g.dir)
			if err != nil {
				return err
			}
			path := filepath.Join(repo.Dir, config.FileName)
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists; pass --force to replace it", config.FileName)
			}
			pkgs, err := setup.Detect(repo.Dir)
			if err != nil {
				return err
			}
			set := setup.Defaults(pkgs)
			if g.provider != "" {
				set.Provider = g.provider
				if g.provider == "codex" {
					set.Model = ""
				}
			}
			if g.model != "" {
				set.Model = g.model
			}

			if !noAI {
				provider, err := ai.New(ctx, &config.Config{Provider: set.Provider, Model: set.Model, Vertex: config.Vertex{Region: "global"}})
				if err != nil {
					return err
				}
				printf(cmd.ErrOrStderr(), "Asking %s to read the repository…\n", provider.Name())
				rc := setup.Gather(ctx, repo, pkgs)
				s, err := setup.Suggest(ctx, provider, rc)
				if err != nil {
					return fmt.Errorf("%w (pass --no-ai to write the file without a model)", err)
				}
				for _, d := range setup.Apply(repo.Dir, rc, s, &set) {
					printf(cmd.ErrOrStderr(), "dropped %s\n", d)
				}
			}

			if err := os.WriteFile(path, []byte(setup.Render(set)), 0o644); err != nil {
				return err
			}
			if _, err := config.Load(repo.Dir); err != nil {
				return fmt.Errorf("the generated file does not load, please report this: %w", err)
			}
			printf(out, "Wrote %s with %d package(s):\n", config.FileName, len(set.Packages))
			for _, p := range set.Packages {
				printf(out, "  %s (%s) at %s\n", p.Name, p.Ecosystem, p.Path)
			}
			if set.Summary != "" {
				printf(out, "\n%s\n", set.Summary)
			}
			printf(out, "\nReview the file, then run `vesnik plan` to see what the next release would be.\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing "+config.FileName)
	cmd.Flags().BoolVar(&noAI, "no-ai", false, "detect packages only; do not ask a model")
	return cmd
}
