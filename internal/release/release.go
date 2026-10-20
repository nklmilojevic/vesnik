// Package release plans and executes a release: it finds what changed in
// each package, asks the model for notes and a bump, and after a human
// accepts the plan, writes versions and changelogs, commits, tags, pushes,
// and publishes GitHub Releases.
package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"golang.org/x/sync/errgroup"

	"github.com/nklmilojevic/vesnik/internal/ai"
	"github.com/nklmilojevic/vesnik/internal/collect"
	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/ecosystem"
	"github.com/nklmilojevic/vesnik/internal/forge/github"
	"github.com/nklmilojevic/vesnik/internal/gitx"
	"github.com/nklmilojevic/vesnik/internal/prompt"
	"github.com/nklmilojevic/vesnik/internal/version"
)

// Forge is the GitHub API surface a release needs.
type Forge interface {
	collect.PRSource
	GenerateNotes(ctx context.Context, tag, target, previous string) (string, error)
	CreateRelease(ctx context.Context, r github.Release) (github.Release, error)
	ReleaseByTag(ctx context.Context, tag string) (github.Release, error)
	UpdateReleaseBody(ctx context.Context, id int64, body string) (github.Release, error)
}

// Env holds what every step needs.
type Env struct {
	Repo  gitx.Repo
	Cfg   *config.Config
	Forge Forge
	AI    ai.Provider
	// Project names the product in prompts; it defaults to the repo name.
	Project string
	Log     io.Writer
	Now     func() time.Time
}

func (e *Env) logf(format string, args ...any) {
	if e.Log != nil {
		_, _ = fmt.Fprintf(e.Log, format+"\n", args...)
	}
}

// Options select packages and override the model's choices.
type Options struct {
	Packages []string
	// Bump overrides the model's suggestion for every package.
	Bump version.Bump
	// Pre makes pre-releases with this identifier, e.g. "rc".
	Pre string
}

// Item is the release of one package.
type Item struct {
	Pkg      config.Package
	Template version.Template
	Bumper   ecosystem.Bumper
	// Changelog is the changelog path relative to the repository root, or
	// empty when changelogs are off.
	Changelog string

	// Current is the latest release, LastFinal the latest final release.
	Current, LastFinal       *semver.Version
	CurrentTag, LastFinalTag string
	// Base is the tag the notes start from: the last final release for a
	// final version, so promoted pre-releases are covered, or the latest
	// release for a pre-release. Empty means the whole history.
	Base string

	Changes  collect.Set
	Floor    version.Bump
	Proposal ai.Proposal

	Bump  version.Bump
	Next  *semver.Version
	Tag   string
	Notes string
	Skip  bool
	// Why explains a skip.
	Why string
	// Released is the tag that was actually published, when replaying a
	// past release.
	Released string
	// AIError is set when the model failed and ai.on_error is plain; the
	// release then carries GitHub's generated notes only.
	AIError error
}

// Warning flags a bump below what the commit types call for.
func (it *Item) Warning() string {
	if it.Bump.Rank() < it.Floor.Rank() {
		return fmt.Sprintf("the commits call for at least a %s bump", it.Floor)
	}
	return ""
}

// Plan is the whole release.
type Plan struct {
	Items []*Item
	Pre   string
	opts  version.Options
}

// NewPlan builds a plan from items, for callers that assemble one by hand.
func NewPlan(opts version.Options, items ...*Item) *Plan {
	return &Plan{Items: items, Pre: opts.Pre, opts: opts}
}

// Active returns the items that will be released.
func (p *Plan) Active() []*Item {
	var out []*Item
	for _, it := range p.Items {
		if !it.Skip {
			out = append(out, it)
		}
	}
	return out
}

// SetBump changes an item's bump and recomputes its version and tag.
func (p *Plan) SetBump(it *Item, b version.Bump) error {
	next, err := version.Next(it.Current, it.LastFinal, b, p.opts)
	if err != nil {
		return err
	}
	it.Bump, it.Next, it.Tag = b, next, it.Template.Render(next)
	return nil
}

// Plan collects the changes of each package and asks the model about every
// package that changed.
func (e *Env) Plan(ctx context.Context, opts Options) (*Plan, error) {
	pkgs, err := e.Cfg.Find(opts.Packages)
	if err != nil {
		return nil, err
	}
	plan := NewPlan(version.Options{Pre: opts.Pre, BreakingBumpsMinor: *e.Cfg.BreakingBumpsMinor})
	for _, pkg := range pkgs {
		it, err := e.item(ctx, pkg, opts.Pre)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pkg.Name, err)
		}
		plan.Items = append(plan.Items, it)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)
	for _, it := range plan.Active() {
		g.Go(func() error {
			e.logf("Asking %s about %s (%d commits)…", e.AI.Name(), it.Pkg.Name, len(it.Changes.Changes))
			b := it.Floor
			if err := e.Propose(gctx, it); err != nil {
				if e.Cfg.AI.OnError != "plain" {
					return fmt.Errorf("%s: %w", it.Pkg.Name, err)
				}
				it.AIError = err
				e.logf("warning: %s: %v; releasing with GitHub's generated notes", it.Pkg.Name, err)
			} else {
				b = it.Proposal.Bump
			}
			if opts.Bump != "" {
				b = opts.Bump
			}
			return plan.SetBump(it, b)
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return plan, nil
}

// template returns the package's tag template. A default "v{version}"
// template falls back to bare "{version}" tags when only those exist, so a
// repository tagged 0.4.2 is not treated as never released.
func (e *Env) template(ctx context.Context, pkg *config.Package) (version.Template, error) {
	tmpl, err := version.ParseTemplate(pkg.Tag)
	if err != nil || !pkg.DefaultTag {
		return tmpl, err
	}
	bare := strings.Replace(pkg.Tag, "v"+version.Placeholder, version.Placeholder, 1)
	for _, candidate := range []string{pkg.Tag, bare} {
		t, err := version.ParseTemplate(candidate)
		if err != nil {
			return tmpl, err
		}
		tags, err := e.Repo.Tags(ctx, t.Glob())
		if err != nil {
			return tmpl, err
		}
		if latest, _ := t.Latest(tags); latest.Tag != "" {
			pkg.Tag = candidate
			return t, nil
		}
	}
	return tmpl, nil
}

func (e *Env) item(ctx context.Context, pkg config.Package, pre string) (*Item, error) {
	tmpl, err := e.template(ctx, &pkg)
	if err != nil {
		return nil, err
	}
	bumper, err := ecosystem.For(pkg.Ecosystem, filepath.Join(e.Repo.Dir, pkg.Path))
	if err != nil {
		return nil, err
	}
	tags, err := e.Repo.Tags(ctx, tmpl.Glob())
	if err != nil {
		return nil, err
	}
	latest, final := tmpl.Latest(tags)
	it := &Item{
		Pkg: pkg, Template: tmpl, Bumper: bumper,
		Changelog: e.Cfg.Changelog.PathFor(pkg.Path),
		Current:   latest.Version, CurrentTag: latest.Tag,
		LastFinal: final.Version, LastFinalTag: final.Tag,
	}
	it.Base = final.Tag
	if pre != "" {
		it.Base = latest.Tag
	}
	it.Changes, err = collect.Collect(ctx, e.Repo, e.Forge, pkg.Path, it.Base, "HEAD")
	if err != nil {
		return nil, err
	}
	if len(it.Changes.Changes) == 0 {
		it.Skip, it.Why = true, "no changes since "+orFirst(it.Base)
	}
	if n := it.Changes.MissingPRs; n > 0 {
		e.logf("warning: %s: pull request lookup failed for %d of %d commits; those notes use commit messages only",
			pkg.Name, n, len(it.Changes.Changes))
	}
	it.Floor = version.Floor(it.Changes.Messages())
	return it, nil
}

func orFirst(tag string) string {
	if tag == "" {
		return "the first commit"
	}
	return tag
}

// Propose asks the model for the item's notes and bump; regenerating an
// item calls it again.
func (e *Env) Propose(ctx context.Context, it *Item) error {
	in := prompt.Input{
		Project: e.Project,
		Package: it.Pkg,
		Multi:   len(e.Cfg.Packages) > 1,
		Current: it.Current,
		LastTag: it.CurrentTag,
		Floor:   it.Floor,
		Changes: it.Changes,
	}
	system, err := prompt.System(e.Repo.Dir, e.Cfg.Style, in)
	if err != nil {
		return err
	}
	p, err := ai.Propose(ctx, e.AI, ai.Request{System: system, User: prompt.User(in)})
	if err != nil {
		return err
	}
	it.Proposal, it.Notes = p, p.Notes
	return nil
}

// Render describes the plan for --dry-run and the review screen.
func (p *Plan) Render() string {
	var b strings.Builder
	for _, it := range p.Items {
		if it.Skip {
			fmt.Fprintf(&b, "%s: skipped (%s)\n\n", it.Pkg.Name, it.Why)
			continue
		}
		from := "unreleased"
		if it.Current != nil {
			from = it.Current.String()
		}
		fmt.Fprintf(&b, "%s: %s → %s (%s bump), tag %s\n", it.Pkg.Name, from, it.Next, it.Bump, it.Tag)
		if it.Released != "" {
			match := "same as vesnik's pick"
			if it.Released != it.Tag {
				match = "vesnik would pick " + it.Tag
			}
			fmt.Fprintf(&b, "  released as %s (%s)\n", it.Released, match)
		}
		if w := it.Warning(); w != "" {
			fmt.Fprintf(&b, "  warning: %s\n", w)
		}
		if it.AIError != nil {
			fmt.Fprintf(&b, "  warning: AI unavailable (%v); the release uses GitHub's generated notes\n", it.AIError)
		}
		if it.Proposal.Reasoning != "" {
			fmt.Fprintf(&b, "  why: %s\n", it.Proposal.Reasoning)
		}
		fmt.Fprintf(&b, "  notes since %s, %d commits\n", orFirst(it.Base), len(it.Changes.Changes))
		files := strings.Join(it.Files(), ", ")
		if files == "" {
			files = "none, tag only"
		}
		fmt.Fprintf(&b, "  files: %s\n\n", files)
		b.WriteString(indent(it.Notes, "    "))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// Files lists the repository files the release changes for this item.
func (it *Item) Files() []string {
	var files []string
	for _, f := range it.Bumper.Files(it.Pkg.Path) {
		files = append(files, filepath.ToSlash(filepath.Join(it.Pkg.Path, f)))
	}
	for _, r := range it.Pkg.Files {
		files = append(files, filepath.ToSlash(filepath.Join(it.Pkg.Path, r.File)))
	}
	if it.Changelog != "" {
		files = append(files, it.Changelog)
	}
	return files
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

// ErrNothing means no package has changes to release.
var ErrNothing = errors.New("nothing to release")

// ErrAborted means the human aborted the release in the review.
var ErrAborted = errors.New("aborted; nothing was changed")

// ChangelogNotes returns the notes for the changelog. Without AI notes it
// lists the commit subjects, so a changelog never gets an empty section.
func (it *Item) ChangelogNotes() string {
	if strings.TrimSpace(it.Notes) != "" {
		return it.Notes
	}
	var b strings.Builder
	for _, c := range it.Changes.Changes {
		fmt.Fprintf(&b, "- %s\n", c.Commit.Subject)
	}
	return b.String()
}
