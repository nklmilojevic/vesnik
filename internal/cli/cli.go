// Package cli wires the vesnik commands.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/nklmilojevic/vesnik/internal/ai"
	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/forge/github"
	"github.com/nklmilojevic/vesnik/internal/gitx"
	"github.com/nklmilojevic/vesnik/internal/release"
)

type globals struct {
	dir      string
	provider string
	model    string
}

// Execute runs the root command.
func Execute(ctx context.Context, version string, args []string, stdout, stderr io.Writer) error {
	root := newRoot(version, stdout, stderr)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}

func newRoot(version string, stdout, stderr io.Writer) *cobra.Command {
	g := &globals{}
	root := &cobra.Command{
		Use:   "vesnik",
		Short: "AI-written release notes and version bumps, reviewed by a human before anything ships",
		Long: `vesnik cuts releases. It reads the commits and pull requests since the last
tag, asks a model for release notes and a version bump, and shows both for
review. After you accept, it bumps version files, updates CHANGELOG.md,
commits, tags, pushes, and publishes a GitHub Release.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       resolveVersion(version),
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().StringVarP(&g.dir, "dir", "C", ".", "run as if vesnik was started in this directory")
	root.PersistentFlags().StringVar(&g.provider, "provider", "",
		"model provider: claude, codex, anthropic or vertex (overrides .vesnik.yaml)")
	root.PersistentFlags().StringVar(&g.model, "model", "", "model name (overrides .vesnik.yaml)")

	root.AddCommand(
		newReleaseCmd(g, false),
		newReleaseCmd(g, true),
		newNotesCmd(g),
		newRenoteCmd(g),
		newInitCmd(g),
	)
	return root
}

func resolveVersion(v string) string {
	if v != "" && v != "dev" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// env loads the repository, config, GitHub client and model provider.
func (g *globals) env(ctx context.Context, stderr io.Writer) (*release.Env, error) {
	repo, err := gitx.Open(ctx, g.dir)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(repo.Dir)
	if err != nil {
		return nil, err
	}
	if g.provider != "" {
		if cfg.Provider != g.provider && g.model == "" {
			// The configured model belongs to the configured provider; codex
			// picks its own default.
			cfg.Model = config.DefaultModel
			if g.provider == "codex" {
				cfg.Model = ""
			}
		}
		cfg.Provider = g.provider
	}
	if g.model != "" {
		cfg.Model = g.model
	}
	owner, name, err := repo.GitHubRepo(ctx, cfg.Remote)
	if err != nil {
		return nil, err
	}
	forge, err := github.New(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	provider, err := ai.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &release.Env{Repo: repo, Cfg: cfg, Forge: forge, AI: provider, Project: name, Log: stderr, Now: now}, nil
}

// isTerminal is a real isatty check: /dev/null is a character device too.
func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func printf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
