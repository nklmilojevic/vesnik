package cli

import (
	"errors"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/nklmilojevic/vesnik/internal/release"
	"github.com/nklmilojevic/vesnik/internal/tui"
	"github.com/nklmilojevic/vesnik/internal/version"
)

var now = time.Now

func newReleaseCmd(g *globals, planOnly bool) *cobra.Command {
	var (
		bump     string
		pre      string
		dryRun   bool
		yes      bool
		packages []string
		at       string
	)
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Plan, review and publish a release",
		Long: `Plan a release for every package with changes, review the notes and bumps
in a terminal UI, then bump, commit, tag, push and publish.

Keys in the review: enter accepts, e edits the notes in $EDITOR, r asks the
model again, b changes the bump, s skips a package, q aborts.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			dryRun = dryRun || planOnly
			if at != "" {
				return replay(cmd, g, packages, at)
			}
			opts := release.Options{Packages: packages, Pre: pre}
			if bump != "" {
				b, err := version.ParseBump(bump)
				if err != nil {
					return err
				}
				opts.Bump = b
			}
			if !dryRun && !yes && !isTerminal(os.Stdin) {
				return errors.New("the review needs a terminal; run it interactively, or pass --yes to accept the model's plan")
			}
			env, err := g.env(ctx, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			var branch string
			if !dryRun {
				if branch, err = env.Preflight(ctx); err != nil {
					return err
				}
			}
			plan, err := env.Plan(ctx, opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(plan.Active()) == 0 {
				printf(out, "%sNothing to release.\n", plan.Render())
				return nil
			}
			if dryRun {
				printf(out, "%s", plan.Render())
				return nil
			}
			if yes {
				printf(out, "%s", plan.Render())
			} else {
				accepted, err := tui.Run(ctx, plan, env)
				if err != nil {
					return err
				}
				if !accepted {
					return release.ErrAborted
				}
			}
			results, err := env.Execute(ctx, plan, branch)
			for _, r := range results {
				if r.Err != nil {
					printf(out, "✗ %s: %v\n", r.Tag, r.Err)
				} else {
					printf(out, "✓ %s %s\n", r.Tag, r.URL)
				}
			}
			return err
		},
	}
	if planOnly {
		cmd.Use = "plan"
		cmd.Short = "Show what release would do, without changing anything"
		cmd.Long = "Show the release plan with the model's notes and bumps. Nothing is written, tagged or pushed."
	}
	f := cmd.Flags()
	f.StringVar(&bump, "bump", "", "use this bump for every package instead of the model's: patch, minor or major")
	f.StringVar(&pre, "pre", "", "make a pre-release with this identifier, e.g. rc or beta; release without it to promote")
	f.StringSliceVarP(&packages, "package", "p", nil, "release only these packages (repeatable)")
	if planOnly {
		f.StringVar(&at, "at", "", "replay a past release: plan the changes that led to this tag and compare with what was released")
	} else {
		f.BoolVar(&dryRun, "dry-run", false, "print the plan and change nothing")
		f.BoolVarP(&yes, "yes", "y", false, "skip the review and accept the model's plan")
	}
	return cmd
}

func replay(cmd *cobra.Command, g *globals, packages []string, tag string) error {
	if len(packages) > 1 {
		return errors.New("--at replays one package; pass at most one --package")
	}
	env, err := g.env(cmd.Context(), cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	pkg := ""
	if len(packages) == 1 {
		pkg = packages[0]
	}
	plan, err := env.Replay(cmd.Context(), pkg, tag)
	if err != nil {
		return err
	}
	printf(cmd.OutOrStdout(), "%s", plan.Render())
	return nil
}

func newNotesCmd(g *globals) *cobra.Command {
	var pkg string
	cmd := &cobra.Command{
		Use:   "notes [tag]",
		Short: "Print release notes for a tag, or for the unreleased commits",
		Long: `Print release notes. With a tag, the notes cover the changes since the
previous final release (or the previous release, for a pre-release tag).
Without one, they cover the commits since the last final release.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := g.env(cmd.Context(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			tag := ""
			if len(args) == 1 {
				tag = args[0]
			}
			it, err := env.Notes(cmd.Context(), pkg, tag)
			if err != nil {
				return err
			}
			printf(cmd.OutOrStdout(), "%s\n", it.Notes)
			return nil
		},
	}
	cmd.Flags().StringVarP(&pkg, "package", "p", "", "package to write notes for, in a repository with several")
	return cmd
}

func newRenoteCmd(g *globals) *cobra.Command {
	var pkg string
	cmd := &cobra.Command{
		Use:   "renote <tag>",
		Short: "Regenerate a published release's notes and replace its description",
		Long:  "Regenerate the notes of a tag and replace its GitHub Release description, or create the release when it is missing.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := g.env(cmd.Context(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			it, rel, err := env.Renote(cmd.Context(), pkg, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			printf(out, "%s\n\n✓ %s %s\n", it.Notes, args[0], rel.URL)
			return nil
		},
	}
	cmd.Flags().StringVarP(&pkg, "package", "p", "", "package the tag belongs to, in a repository with several")
	return cmd
}
