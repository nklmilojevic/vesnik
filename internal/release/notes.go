package release

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/nklmilojevic/vesnik/internal/collect"
	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/ecosystem"
	"github.com/nklmilojevic/vesnik/internal/forge/github"
	"github.com/nklmilojevic/vesnik/internal/version"
)

// Notes writes the notes for one package: for tag when it is set, or for
// the unreleased commits since the last final release.
func (e *Env) Notes(ctx context.Context, pkgName, tag string) (*Item, error) {
	pkg, err := e.single(pkgName)
	if err != nil {
		return nil, err
	}
	if tag == "" {
		it, err := e.item(ctx, e.Forge, pkg, "")
		if err != nil {
			return nil, err
		}
		if it.Skip {
			return nil, fmt.Errorf("%s: %s", pkg.Name, it.Why)
		}
		return it, e.Propose(ctx, it)
	}

	tmpl, err := e.template(ctx, &pkg)
	if err != nil {
		return nil, err
	}
	v, ok := tmpl.Parse(tag)
	if !ok {
		return nil, fmt.Errorf("tag %s does not match %s's tag template %s", tag, pkg.Name, pkg.Tag)
	}
	if !e.Repo.RefExists(ctx, tag) {
		return nil, fmt.Errorf("unknown tag %s", tag)
	}
	tags, err := e.Repo.Tags(ctx, tmpl.Glob(), tag)
	if err != nil {
		return nil, err
	}
	var older []string
	for _, t := range tags {
		if tv, ok := tmpl.Parse(t); ok && tv.LessThan(v) {
			older = append(older, t)
		}
	}
	latest, final := tmpl.Latest(older)
	base := final
	if v.Prerelease() != "" {
		base = latest
	}
	dir := filepath.Join(e.Repo.Dir, pkg.Path)
	bumper, err := ecosystem.For(pkg.Ecosystem, dir)
	if err != nil {
		return nil, err
	}
	it := &Item{
		Pkg: pkg, Template: tmpl, Bumper: bumper, Dir: dir,
		Changelog: e.Cfg.Changelog.PathFor(pkg.Path),
		Current:   base.Version, CurrentTag: base.Tag,
		LastFinal: final.Version, LastFinalTag: final.Tag,
		Base: base.Tag, Next: v, Tag: tag,
	}
	it.Changes, err = collect.Collect(ctx, e.Repo, e.Forge, pkg.Path, base.Tag, tag)
	if err != nil {
		return nil, err
	}
	e.warnLookups(pkg.Name, it.Changes)
	if len(it.Changes.Changes) == 0 {
		return nil, fmt.Errorf("%s: no changes between %s and %s", pkg.Name, orFirst(base.Tag), tag)
	}
	it.Floor = version.Floor(it.Changes.Messages())
	return it, e.Propose(ctx, it)
}

// Replay plans a past release again: the changes between the previous tag
// and tag, with the bump and version the model picks now, next to the
// version that was actually released. Nothing is written.
func (e *Env) Replay(ctx context.Context, pkgName, tag string) (*Plan, error) {
	it, err := e.Notes(ctx, pkgName, tag)
	if err != nil {
		return nil, err
	}
	it.Released = it.Tag
	pre, _, _ := strings.Cut(it.Next.Prerelease(), ".")
	plan := NewPlan(version.Options{Pre: pre, BreakingBumpsMinor: *e.Cfg.BreakingBumpsMinor}, it)
	return plan, plan.SetBump(it, it.Proposal.Bump)
}

func (e *Env) single(name string) (config.Package, error) {
	if name == "" {
		if len(e.Cfg.Packages) > 1 {
			return config.Package{}, errors.New("this repository has several packages; pick one with --package")
		}
		return e.Cfg.Packages[0], nil
	}
	pkgs, err := e.Cfg.Find([]string{name})
	if err != nil {
		return config.Package{}, err
	}
	return pkgs[0], nil
}

// Renote regenerates the notes of a published tag and replaces its GitHub
// Release description, creating the release when it is missing.
func (e *Env) Renote(ctx context.Context, pkgName, tag string) (*Item, github.Release, error) {
	it, err := e.Notes(ctx, pkgName, tag)
	if err != nil {
		return nil, github.Release{}, err
	}
	body, err := e.Body(ctx, it.Notes, tag, "", it.Base)
	if err != nil {
		return it, github.Release{}, err
	}
	rel, err := e.Forge.ReleaseByTag(ctx, tag)
	var apiErr *github.Error
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		pre := it.Next.Prerelease() != ""
		r := github.Release{TagName: tag, Name: tag, Body: body, Prerelease: pre}
		if pre {
			r.MakeLatest = "false"
		}
		rel, err = e.Forge.CreateRelease(ctx, r)
		return it, rel, err
	}
	if err != nil {
		return it, github.Release{}, err
	}
	rel, err = e.Forge.UpdateReleaseBody(ctx, rel.ID, body)
	return it, rel, err
}
