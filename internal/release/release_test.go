package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nklmilojevic/vesnik/internal/ai"
	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/forge/github"
	"github.com/nklmilojevic/vesnik/internal/gitx/gittest"
	"github.com/nklmilojevic/vesnik/internal/version"
)

type fakeAI struct {
	mu       sync.Mutex
	bump     version.Bump
	err      error
	requests []ai.Request
}

func (f *fakeAI) Name() string { return "fake" }

func (f *fakeAI) JSON(_ context.Context, req ai.Request) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return json.Marshal(ai.Proposal{Bump: f.bump, Reasoning: "because", Notes: fmt.Sprintf("Notes %d.\n\n## Fixes\n\n- Fixed.", len(f.requests))})
}

type fakeForge struct {
	mu        sync.Mutex
	created   []github.Release
	updated   map[int64]string
	existing  map[string]github.Release
	generated []string
}

func (f *fakeForge) PullsForCommit(context.Context, string) ([]github.PR, error) { return nil, nil }

func (f *fakeForge) GenerateNotes(_ context.Context, tag, _, previous string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generated = append(f.generated, previous+".."+tag)
	return "## What's Changed\n* PR list", nil
}

func (f *fakeForge) CreateRelease(_ context.Context, r github.Release) (github.Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, r)
	r.URL = "https://github.test/" + r.TagName
	return r, nil
}

func (f *fakeForge) ReleaseByTag(_ context.Context, tag string) (github.Release, error) {
	if r, ok := f.existing[tag]; ok {
		return r, nil
	}
	return github.Release{}, &github.Error{Status: http.StatusNotFound, Message: "Not Found"}
}

func (f *fakeForge) UpdateReleaseBody(_ context.Context, id int64, body string) (github.Release, error) {
	if f.updated == nil {
		f.updated = map[int64]string{}
	}
	f.updated[id] = body
	return github.Release{ID: id, Body: body}, nil
}

type fixture struct {
	*gittest.TestRepo
	remote string
	env    *Env
	ai     *fakeAI
	forge  *fakeForge
}

// newFixture builds a repo with a bare origin, writes files, commits them as
// the initial release commit, and tags it with tags.
func newFixture(t *testing.T, cfgYAML string, files map[string]string, tags ...string) *fixture {
	t.Helper()
	tr := gittest.New(t)
	remote := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	tr.Git("remote", "add", "origin", remote)
	if cfgYAML != "" {
		tr.Write(config.FileName, cfgYAML)
	}
	for path, content := range files {
		tr.Write(path, content)
	}
	tr.Git("add", "-A")
	tr.Git("commit", "-q", "-m", "chore: init")
	for _, tag := range tags {
		tr.Git("tag", "-a", tag, "-m", tag)
	}
	tr.Git("push", "-q", "origin", "main", "--tags")
	tr.Git("remote", "set-head", "origin", "main")
	f := &fixture{TestRepo: tr, remote: remote, ai: &fakeAI{bump: version.Minor}, forge: &fakeForge{}}
	f.reload(t)
	return f
}

func (f *fixture) reload(t *testing.T) {
	t.Helper()
	cfg, err := config.Load(f.Dir)
	if err != nil {
		t.Fatal(err)
	}
	f.env = &Env{Repo: f.Repo, Cfg: cfg, Forge: f.forge, AI: f.ai, Project: "demo", Now: func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }}
}

func (f *fixture) commitAndPush(t *testing.T, path, content, msg string) {
	t.Helper()
	f.CommitFile(path, content, msg)
	f.Git("push", "-q", "origin", "main")
}

func (f *fixture) release(t *testing.T, opts Options) (*Plan, []Result) {
	t.Helper()
	ctx := context.Background()
	branch, err := f.env.Preflight(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.env.Plan(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.env.Execute(ctx, plan, branch)
	if err != nil {
		t.Fatal(err)
	}
	return plan, res
}

func (f *fixture) remoteTags(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", f.remote, "tag", "--list").CombinedOutput()
	if err != nil {
		t.Fatalf("%s", out)
	}
	return strings.TrimSpace(string(out))
}

func TestReleaseSinglePackage(t *testing.T) {
	f := newFixture(t, "", map[string]string{"package.json": "{\n  \"name\": \"demo\",\n  \"version\": \"1.0.0\"\n}\n"}, "v1.0.0")
	f.commitAndPush(t, "src/a.js", "1", "feat: add a")

	plan, res := f.release(t, Options{})
	it := plan.Items[0]
	if it.Tag != "v1.1.0" || it.Bump != version.Minor || it.Base != "v1.0.0" {
		t.Fatalf("item = %+v", it)
	}
	if got := f.Read("package.json"); !strings.Contains(got, `"version": "1.1.0"`) {
		t.Fatalf("package.json =\n%s", got)
	}
	if got := f.Read("CHANGELOG.md"); !strings.HasPrefix(got, "# Changelog\n\n## v1.1.0 - 2026-10-20\n\nNotes 1.\n\n### Fixes") {
		t.Fatalf("CHANGELOG.md =\n%s", got)
	}
	if subject := f.Git("log", "-1", "--format=%s"); subject != "chore(release): v1.1.0" {
		t.Fatalf("commit = %q", subject)
	}
	if f.Git("rev-parse", "v1.1.0^{commit}") != f.Git("rev-parse", "HEAD") {
		t.Fatal("tag does not point at the release commit")
	}
	if !strings.Contains(f.remoteTags(t), "v1.1.0") {
		t.Fatal("tag not pushed")
	}
	if clean, _ := f.Clean(context.Background()); !clean {
		t.Fatal("tree dirty after release")
	}
	if len(res) != 1 || res[0].URL != "https://github.test/v1.1.0" {
		t.Fatalf("results = %+v", res)
	}
	rel := f.forge.created[0]
	if rel.Prerelease || rel.Body != "Notes 1.\n\n## Fixes\n\n- Fixed.\n\n## What's Changed\n* PR list" {
		t.Fatalf("release = %+v", rel)
	}
	if f.forge.generated[0] != "v1.0.0..v1.1.0" {
		t.Fatalf("generated = %v", f.forge.generated)
	}
	if !strings.Contains(f.ai.requests[0].User, "### feat: add a") || !strings.Contains(f.ai.requests[0].System, "release notes for demo") {
		t.Fatalf("request = %+v", f.ai.requests[0])
	}
}

func TestReleaseGoIsTagOnlyAndPre1BreakingBumpsMinor(t *testing.T) {
	f := newFixture(t, "", map[string]string{"go.mod": "module x\n"}, "v0.3.0")
	f.commitAndPush(t, "main.go", "package main", "fix: crash")
	f.ai.bump = version.Major
	plan, _ := f.release(t, Options{})
	if plan.Items[0].Tag != "v0.4.0" {
		t.Fatalf("pre-1.0 breaking change should bump minor, got %s", plan.Items[0].Tag)
	}
	if plan.Items[0].Warning() != "" {
		t.Fatalf("warning = %q", plan.Items[0].Warning())
	}
	if f.Read("go.mod") != "module x\n" {
		t.Fatal("go.mod changed")
	}
}

func TestReleaseMonorepoSkipsUnchangedPackage(t *testing.T) {
	cfg := `packages:
  - path: packages/api
    ecosystem: php
  - path: packages/web
`
	f := newFixture(t, cfg, map[string]string{
		"packages/api/composer.json": "{\n    \"name\": \"acme/api\",\n    \"version\": \"2.0.0\"\n}\n",
		"packages/web/package.json":  "{\"name\": \"web\", \"version\": \"1.0.0\"}\n",
	}, "api@v2.0.0", "web@v1.0.0")
	f.commitAndPush(t, "packages/api/src/A.php", "<?php", "fix(api): null user")
	f.ai.bump = version.Patch

	plan, _ := f.release(t, Options{})
	api, web := plan.Items[0], plan.Items[1]
	if api.Tag != "api@v2.0.1" || !web.Skip || !strings.Contains(web.Why, "no changes since web@v1.0.0") {
		t.Fatalf("api = %+v\nweb = %+v", api, web)
	}
	if !strings.Contains(f.Read("packages/api/composer.json"), `"version": "2.0.1"`) {
		t.Fatal("composer.json not bumped")
	}
	if !strings.Contains(f.Read("packages/api/CHANGELOG.md"), "## api@v2.0.1") {
		t.Fatal("api changelog missing")
	}
	if f.Read("packages/web/package.json") != "{\"name\": \"web\", \"version\": \"1.0.0\"}\n" {
		t.Fatal("web changed")
	}
	if !strings.Contains(f.ai.requests[0].User, "package api (path packages/api)") {
		t.Fatalf("prompt = %s", f.ai.requests[0].User)
	}
	if len(f.ai.requests) != 1 {
		t.Fatalf("asked the model %d times", len(f.ai.requests))
	}
}

func TestReleaseCandidateThenPromotion(t *testing.T) {
	f := newFixture(t, "", map[string]string{"README.md": "x"}, "v1.0.0")
	f.commitAndPush(t, "a", "1", "feat: a")

	plan, _ := f.release(t, Options{Pre: "rc"})
	if plan.Items[0].Tag != "v1.1.0-rc.1" || plan.Items[0].Base != "v1.0.0" {
		t.Fatalf("rc1 = %+v", plan.Items[0])
	}
	if rel := f.forge.created[0]; !rel.Prerelease || rel.MakeLatest != "false" {
		t.Fatalf("rc release = %+v", rel)
	}

	f.commitAndPush(t, "b", "1", "fix: b")
	f.ai.bump = version.Patch
	plan, _ = f.release(t, Options{Pre: "rc"})
	if plan.Items[0].Tag != "v1.1.0-rc.2" || plan.Items[0].Base != "v1.1.0-rc.1" {
		t.Fatalf("rc2 = %+v", plan.Items[0])
	}
	if n := len(plan.Items[0].Changes.Changes); n != 1 {
		t.Fatalf("rc2 covers %d commits, want 1", n)
	}

	plan, _ = f.release(t, Options{})
	it := plan.Items[0]
	if it.Tag != "v1.1.0" || it.Base != "v1.0.0" {
		t.Fatalf("final = %+v", it)
	}
	if n := len(it.Changes.Changes); n != 2 {
		t.Fatalf("promotion notes cover %d commits, want both rc commits", n)
	}
	if rel := f.forge.created[2]; rel.Prerelease {
		t.Fatalf("final release = %+v", rel)
	}
}

func TestBumpOverrideAndFloorWarning(t *testing.T) {
	f := newFixture(t, "", map[string]string{"README.md": "x"}, "v1.0.0")
	f.commitAndPush(t, "a", "1", "feat!: drop the old flag")
	f.ai.bump = version.Minor
	plan, err := f.env.Plan(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	it := plan.Items[0]
	if it.Floor != version.Major || it.Warning() == "" || !strings.Contains(plan.Render(), "warning: the commits call for at least a major bump") {
		t.Fatalf("item = %+v\n%s", it, plan.Render())
	}
	if err := plan.SetBump(it, version.Major); err != nil || it.Tag != "v2.0.0" || it.Warning() != "" {
		t.Fatalf("after SetBump: %+v, %v", it, err)
	}

	plan, err = f.env.Plan(context.Background(), Options{Bump: version.Patch})
	if err != nil || plan.Items[0].Tag != "v1.0.1" {
		t.Fatalf("override: %+v, %v", plan.Items[0], err)
	}
}

func TestExecuteRollsBackOnFailure(t *testing.T) {
	cfg := "packages: [{files: [{file: VERSION, regex: 'version=([0-9.]+)'}]}]\n"
	f := newFixture(t, cfg, map[string]string{"VERSION": "no version here\n"}, "v1.0.0")
	f.commitAndPush(t, "a", "1", "fix: a")
	ctx := context.Background()
	branch, err := f.env.Preflight(ctx)
	if err != nil {
		t.Fatal(err)
	}
	head := f.Git("rev-parse", "HEAD")
	plan, err := f.env.Plan(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.env.Execute(ctx, plan, branch)
	if err == nil || !strings.Contains(err.Error(), "nothing was pushed") {
		t.Fatalf("err = %v", err)
	}
	if f.Git("rev-parse", "HEAD") != head {
		t.Fatal("HEAD moved")
	}
	if clean, _ := f.Clean(ctx); !clean {
		t.Fatal("tree left dirty")
	}
	if tags := f.Git("tag", "--list", "v1.0.1"); tags != "" {
		t.Fatal("tag left behind")
	}
	if len(f.forge.created) != 0 {
		t.Fatal("release created")
	}
}

func TestPreflight(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "", map[string]string{"a": "1"})

	f.Write("a", "2")
	if _, err := f.env.Preflight(ctx); err == nil || !strings.Contains(err.Error(), "tracked files") {
		t.Fatalf("dirty: %v", err)
	}
	f.Git("checkout", "a")

	f.Git("switch", "-q", "-c", "topic")
	if _, err := f.env.Preflight(ctx); err == nil || !strings.Contains(err.Error(), "releases are cut from main") {
		t.Fatalf("branch: %v", err)
	}
	f.Git("switch", "-q", "main")

	// Someone else pushed: preflight fast-forwards.
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "clone", "-q", f.remote, other).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	for _, args := range [][]string{
		{"-c", "user.name=O", "-c", "user.email=o@x", "commit", "-q", "--allow-empty", "-m", "fix: theirs"},
		{"push", "-q", "origin", "main"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = other
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s", out)
		}
	}
	if _, err := f.env.Preflight(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Git("log", "-1", "--format=%s") != "fix: theirs" {
		t.Fatal("did not fast-forward")
	}

	// A local commit that is not pushed blocks the release.
	f.CommitFile("b", "1", "fix: mine")
	if _, err := f.env.Preflight(ctx); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unpushed: %v", err)
	}
}

func TestNotesAndRenote(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "", map[string]string{"a": "1"}, "v1.0.0")
	f.CommitFile("b", "1", "feat: b")
	f.Git("tag", "v1.1.0-rc.1")
	f.CommitFile("c", "1", "fix: c")
	f.Git("tag", "v1.1.0")
	f.CommitFile("d", "1", "fix: d")

	it, err := f.env.Notes(ctx, "", "v1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if it.Base != "v1.0.0" || len(it.Changes.Changes) != 2 {
		t.Fatalf("final tag notes: base %s, %d commits", it.Base, len(it.Changes.Changes))
	}
	it, err = f.env.Notes(ctx, "", "v1.1.0-rc.1")
	if err != nil || it.Base != "v1.0.0" || len(it.Changes.Changes) != 1 {
		t.Fatalf("rc notes: %+v, %v", it, err)
	}
	it, err = f.env.Notes(ctx, "", "")
	if err != nil || it.Base != "v1.1.0" || len(it.Changes.Changes) != 1 {
		t.Fatalf("unreleased notes: %+v, %v", it, err)
	}
	if _, err := f.env.Notes(ctx, "", "v9.9.9"); err == nil {
		t.Fatal("want error for an unknown tag")
	}
	if _, err := f.env.Notes(ctx, "", "release-1"); err == nil {
		t.Fatal("want error for a tag outside the template")
	}

	_, rel, err := f.env.Renote(ctx, "", "v1.1.0-rc.1")
	if err != nil || !rel.Prerelease || len(f.forge.created) != 1 {
		t.Fatalf("renote create: %+v, %v", rel, err)
	}
	f.forge.existing = map[string]github.Release{"v1.1.0": {ID: 5, TagName: "v1.1.0"}}
	if _, _, err := f.env.Renote(ctx, "", "v1.1.0"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(f.forge.updated[5], "## What's Changed\n* PR list") {
		t.Fatalf("updated body = %q", f.forge.updated[5])
	}
}

func TestDefaultTagAdoptsBareVersionTags(t *testing.T) {
	f := newFixture(t, "", map[string]string{"go.mod": "module x\n"}, "0.0.63", "0.0.64")
	f.commitAndPush(t, "a.go", "package x", "fix: a")
	f.ai.bump = version.Patch
	plan, _ := f.release(t, Options{})
	if it := plan.Items[0]; it.Tag != "0.0.65" || it.Base != "0.0.64" {
		t.Fatalf("item = %+v", it)
	}

	it, err := f.env.Notes(context.Background(), "", "0.0.65")
	if err != nil || it.Base != "0.0.64" {
		t.Fatalf("notes: %+v, %v", it, err)
	}
}

func TestExplicitTagTemplateIsKept(t *testing.T) {
	f := newFixture(t, "packages: [{tag: 'v{version}'}]\n", map[string]string{"go.mod": "module x\n"}, "0.0.64")
	f.commitAndPush(t, "a.go", "package x", "fix: a")
	plan, err := f.env.Plan(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if it := plan.Items[0]; it.Current != nil || it.Tag != "v0.1.0" {
		t.Fatalf("an explicit template must not adopt bare tags: %+v", it)
	}
}

func TestReplayComparesWithTheReleasedTag(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "", map[string]string{"package.json": `{"version": "1.0.0"}`}, "v1.0.0")
	f.CommitFile("a", "1", "feat: a")
	f.Git("tag", "v1.0.1")
	f.CommitFile("b", "1", "fix: b")
	f.Git("tag", "v1.1.0-rc.1")

	f.ai.bump = version.Minor
	plan, err := f.env.Replay(ctx, "", "v1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	it := plan.Items[0]
	if it.Tag != "v1.1.0" || it.Released != "v1.0.1" || it.Base != "v1.0.0" {
		t.Fatalf("replay = %+v", it)
	}
	if out := plan.Render(); !strings.Contains(out, "released as v1.0.1 (vesnik would pick v1.1.0)") {
		t.Fatalf("render:\n%s", out)
	}

	f.ai.bump = version.Patch
	plan, err = f.env.Replay(ctx, "", "v1.1.0-rc.1")
	if err != nil {
		t.Fatal(err)
	}
	if it := plan.Items[0]; it.Tag != "v1.0.2-rc.1" || it.Base != "v1.0.1" {
		t.Fatalf("rc replay = %+v", it)
	}
	if clean, _ := f.Clean(ctx); !clean || f.Git("tag", "--list", "v1.0.2*") != "" {
		t.Fatal("replay changed the repository")
	}
}

func TestTagOnlyReleaseWithoutChangelogMakesNoCommit(t *testing.T) {
	cfg := "branch: main\nchangelog: {enabled: false}\n"
	f := newFixture(t, cfg, map[string]string{"composer.json": `{"name": "acme/app"}`, "package.json": `{"private": true}`}, "v2.4.7")
	f.commitAndPush(t, "app/A.php", "<?php", "ABC-123: Fix the login redirect")
	head := f.Git("rev-parse", "HEAD")
	f.ai.bump = version.Patch
	plan, _ := f.release(t, Options{Bump: version.Minor})
	it := plan.Items[0]
	if !strings.Contains(plan.Render(), "files: none, tag only") {
		t.Fatalf("render:\n%s", plan.Render())
	}
	if it.Tag != "v2.5.0" || len(it.Files()) != 0 {
		t.Fatalf("item = %+v, files %v", it, it.Files())
	}
	if f.Git("rev-parse", "HEAD") != head || f.Git("rev-parse", "v2.5.0^{commit}") != head {
		t.Fatal("a tag-only release must tag the existing HEAD without a commit")
	}
	if _, err := os.Stat(filepath.Join(f.Dir, "CHANGELOG.md")); err == nil {
		t.Fatal("changelog written while disabled")
	}
	if !strings.Contains(f.remoteTags(t), "v2.5.0") || len(f.forge.created) != 1 {
		t.Fatal("tag not pushed or release not created")
	}
}

func TestPlainFallbackReleasesWithGeneratedNotes(t *testing.T) {
	cfg := "ai: {on_error: plain}\n"
	f := newFixture(t, cfg, map[string]string{"composer.json": `{"name": "acme/app"}`}, "v2.4.7")
	f.commitAndPush(t, "a.php", "<?php", "ABC-123: Fix the login redirect")
	f.ai.err = errors.New("claude is not on PATH")

	plan, _ := f.release(t, Options{Bump: version.Minor})
	it := plan.Items[0]
	if it.Tag != "v2.5.0" || it.AIError == nil || it.Notes != "" {
		t.Fatalf("item = %+v", it)
	}
	if out := plan.Render(); !strings.Contains(out, "AI unavailable (claude is not on PATH)") {
		t.Fatalf("render:\n%s", out)
	}
	if body := f.forge.created[0].Body; body != "## What's Changed\n* PR list" {
		t.Fatalf("body = %q, want only GitHub's generated notes", body)
	}
	if got := f.Read("CHANGELOG.md"); !strings.Contains(got, "- ABC-123: Fix the login redirect") {
		t.Fatalf("changelog without AI notes:\n%s", got)
	}
}

func TestPlainFallbackWithoutBumpUsesCommitTypes(t *testing.T) {
	f := newFixture(t, "ai: {on_error: plain}\n", map[string]string{"go.mod": "module x\n"}, "v1.0.0")
	f.commitAndPush(t, "a.go", "package x", "feat: a")
	f.ai.err = errors.New("timeout")
	plan, err := f.env.Plan(context.Background(), Options{})
	if err != nil || plan.Items[0].Tag != "v1.1.0" {
		t.Fatalf("plan = %+v, %v", plan.Items[0], err)
	}
}

func TestAIErrorFailsByDefault(t *testing.T) {
	f := newFixture(t, "", map[string]string{"go.mod": "module x\n"}, "v1.0.0")
	f.commitAndPush(t, "a.go", "package x", "fix: a")
	f.ai.err = errors.New("rate limited")
	if _, err := f.env.Plan(context.Background(), Options{Bump: version.Patch}); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishFailureIsAPublishError(t *testing.T) {
	f := newFixture(t, "", map[string]string{"go.mod": "module x\n"}, "v1.0.0")
	f.commitAndPush(t, "a.go", "package x", "fix: a")
	ctx := context.Background()
	branch, err := f.env.Preflight(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.env.Plan(ctx, Options{Bump: version.Patch})
	if err != nil {
		t.Fatal(err)
	}
	f.env.Forge = failingForge{f.forge}
	_, err = f.env.Execute(ctx, plan, branch)
	if _, ok := errors.AsType[*PublishError](err); !ok {
		t.Fatalf("err = %v, want *PublishError", err)
	}
	if !strings.Contains(f.remoteTags(t), "v1.0.1") {
		t.Fatal("the tag should be pushed before publishing fails")
	}
}

type failingForge struct{ *fakeForge }

func (failingForge) CreateRelease(context.Context, github.Release) (github.Release, error) {
	return github.Release{}, errors.New("502 Bad Gateway")
}
