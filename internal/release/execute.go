package release

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nklmilojevic/vesnik/internal/changelog"
	"github.com/nklmilojevic/vesnik/internal/ecosystem"
	"github.com/nklmilojevic/vesnik/internal/forge/github"
)

// Preflight checks that the release starts from a clean, up-to-date release
// branch, fast-forwarding it when the remote is ahead. It returns the branch.
func (e *Env) Preflight(ctx context.Context) (string, error) {
	clean, err := e.Repo.Clean(ctx)
	if err != nil {
		return "", err
	}
	if !clean {
		return "", errors.New("tracked files have changes; commit or stash them first")
	}
	want := e.Cfg.Branch
	if want == "" {
		if want, err = e.Repo.DefaultBranch(ctx); err != nil {
			return "", err
		}
	}
	branch, err := e.Repo.Branch(ctx)
	if err != nil {
		return "", err
	}
	if branch != want {
		return "", fmt.Errorf("on branch %s; releases are cut from %s", branch, want)
	}
	remote := e.Cfg.Remote
	if err := e.Repo.Fetch(ctx, remote, branch); err != nil {
		return "", err
	}
	if err := e.Repo.PullFF(ctx, remote, branch); err != nil {
		return "", fmt.Errorf("%s has commits that %s/%s does not; push or drop them first: %w", branch, remote, branch, err)
	}
	local, err := e.Repo.Head(ctx)
	if err != nil {
		return "", err
	}
	upstream, err := e.Repo.Run(ctx, "rev-parse", remote+"/"+branch)
	if err != nil {
		return "", err
	}
	if local != upstream {
		return "", fmt.Errorf("local %s does not match %s/%s", branch, remote, branch)
	}
	return branch, nil
}

// PublishError means the tags were pushed but some GitHub Releases were not
// created. The repository changed, so callers must not fall back to another
// release path; `vesnik renote <tag>` creates the missing releases.
type PublishError struct {
	Failed, Total int
}

func (e *PublishError) Error() string {
	return fmt.Sprintf("%d of %d releases failed after the tags were pushed; run `vesnik renote <tag>` to create them",
		e.Failed, e.Total)
}

// Result is one published release.
type Result struct {
	Tag string
	URL string
	Err error
}

// Execute writes, commits, tags, pushes, and publishes the plan. Any failure
// before the push restores the branch and deletes the new tags.
func (e *Env) Execute(ctx context.Context, plan *Plan, branch string) ([]Result, error) {
	items := plan.Active()
	if len(items) == 0 {
		return nil, ErrNothing
	}
	start, err := e.Repo.Head(ctx)
	if err != nil {
		return nil, err
	}
	var tags []string
	rollback := func(cause error) error {
		for _, t := range tags {
			_ = e.Repo.DeleteTag(ctx, t)
		}
		if err := e.Repo.ResetHard(ctx, start); err != nil {
			return fmt.Errorf("%w; rollback also failed, reset to %s by hand: %v", cause, start, err)
		}
		return fmt.Errorf("%w (nothing was pushed; the branch is back at %.12s)", cause, start)
	}

	date := e.Now().Format("2006-01-02")
	for _, it := range items {
		dir := filepath.Join(e.Repo.Dir, it.Pkg.Path)
		v := it.Next.String()
		if err := it.Bumper.Write(ctx, dir, v); err != nil {
			return nil, rollback(fmt.Errorf("%s: %w", it.Pkg.Name, err))
		}
		if err := ecosystem.WriteRules(dir, it.Pkg.Files, v); err != nil {
			return nil, rollback(fmt.Errorf("%s: %w", it.Pkg.Name, err))
		}
		if it.Changelog == "" {
			continue
		}
		if err := changelog.Prepend(filepath.Join(e.Repo.Dir, it.Changelog), changelog.Section(it.Tag, date, it.ChangelogNotes())); err != nil {
			return nil, rollback(fmt.Errorf("%s: %w", it.Pkg.Name, err))
		}
	}

	if err := e.commit(ctx, items); err != nil {
		return nil, rollback(err)
	}
	for _, it := range items {
		if err := e.Repo.Tag(ctx, it.Tag, "Release "+it.Tag); err != nil {
			return nil, rollback(err)
		}
		tags = append(tags, it.Tag)
	}

	e.logf("Pushing %s and %s…", branch, strings.Join(tags, ", "))
	if err := e.Repo.Push(ctx, e.Cfg.Remote, branch, tags...); err != nil {
		return nil, rollback(err)
	}
	target, err := e.Repo.Head(ctx)
	if err != nil {
		return nil, err
	}

	var results []Result
	var failed int
	for _, it := range items {
		rel, err := e.publish(ctx, it, target)
		results = append(results, Result{Tag: it.Tag, URL: rel.URL, Err: err})
		if err != nil {
			failed++
		}
	}
	if failed > 0 {
		return results, &PublishError{Failed: failed, Total: len(items)}
	}
	return results, nil
}

func (e *Env) commit(ctx context.Context, items []*Item) error {
	for _, it := range items {
		if it.Changelog == "" {
			continue
		}
		if err := e.Repo.Add(ctx, it.Changelog); err != nil {
			return err
		}
	}
	if err := e.Repo.AddUpdated(ctx); err != nil {
		return err
	}
	staged, err := e.Repo.Staged(ctx)
	if err != nil || !staged {
		return err
	}
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Tag
	}
	return e.Repo.Commit(ctx, "chore(release): "+strings.Join(names, ", "))
}

// Body returns the GitHub Release description: the notes, then GitHub's
// generated pull request list when the config asks for it.
func (e *Env) Body(ctx context.Context, notes, tag, target, previous string) (string, error) {
	if !*e.Cfg.GitHub.AppendGeneratedNotes {
		return notes, nil
	}
	gen, err := e.Forge.GenerateNotes(ctx, tag, target, previous)
	if err != nil {
		return "", fmt.Errorf("generated notes: %w", err)
	}
	gen = strings.TrimSpace(gen)
	switch {
	case gen == "":
		return notes, nil
	case strings.TrimSpace(notes) == "":
		return gen, nil
	}
	return notes + "\n\n" + gen, nil
}

func (e *Env) publish(ctx context.Context, it *Item, target string) (github.Release, error) {
	body, err := e.Body(ctx, it.Notes, it.Tag, target, it.Base)
	if err != nil {
		return github.Release{}, err
	}
	rel := github.Release{TagName: it.Tag, Name: it.Tag, Body: body, Prerelease: it.Next.Prerelease() != ""}
	if rel.Prerelease {
		rel.MakeLatest = "false"
	}
	return e.Forge.CreateRelease(ctx, rel)
}
