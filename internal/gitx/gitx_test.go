package gitx_test

import (
	"context"
	"testing"

	"github.com/nklmilojevic/vesnik/internal/gitx/gittest"
)

func TestLogScopesByPathAndKeepsBodies(t *testing.T) {
	ctx := context.Background()
	tr := gittest.New(t)
	tr.CommitFile("README.md", "x", "chore: init")
	tr.Git("tag", "v0.1.0")
	tr.CommitFile("api/a.php", "a", "feat(api): add endpoint\n\nIt lists users.\nSecond line.")
	tr.CommitFile("web/b.ts", "b", "fix(web): button")

	all, err := tr.Log(ctx, "v0.1.0", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Subject != "feat(api): add endpoint" {
		t.Fatalf("Log = %+v", all)
	}
	if all[0].Body != "It lists users.\nSecond line." {
		t.Fatalf("body = %q", all[0].Body)
	}

	api, err := tr.Log(ctx, "v0.1.0", "HEAD", "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(api) != 1 || api[0].Subject != "feat(api): add endpoint" {
		t.Fatalf("scoped Log = %+v", api)
	}

	whole, err := tr.Log(ctx, "", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 3 {
		t.Fatalf("whole history = %d commits", len(whole))
	}
}

func TestTagsCleanAndBranch(t *testing.T) {
	ctx := context.Background()
	tr := gittest.New(t)
	tr.CommitFile("a", "1", "chore: init")
	if err := tr.Tag(ctx, "api@v1.0.0", "api v1.0.0"); err != nil {
		t.Fatal(err)
	}
	tr.Git("tag", "web@v2.0.0")
	tags, err := tr.Tags(ctx, "api@v*")
	if err != nil || len(tags) != 1 || tags[0] != "api@v1.0.0" {
		t.Fatalf("Tags = %v, %v", tags, err)
	}
	clean, err := tr.Clean(ctx)
	if err != nil || !clean {
		t.Fatalf("Clean = %v, %v", clean, err)
	}
	tr.Write("a", "2")
	if clean, _ := tr.Clean(ctx); clean {
		t.Fatal("modified tracked file reported clean")
	}
	tr.Write("untracked", "x")
	tr.Git("checkout", "a")
	if clean, _ := tr.Clean(ctx); !clean {
		t.Fatal("untracked file made the tree dirty")
	}
	if b, err := tr.Branch(ctx); err != nil || b != "main" {
		t.Fatalf("Branch = %q, %v", b, err)
	}
}

func TestGitHubRepo(t *testing.T) {
	ctx := context.Background()
	for url, want := range map[string]string{
		"git@github.com:nklmilojevic/sofka.git":     "nklmilojevic/sofka",
		"https://github.com/nklmilojevic/vesnik":    "nklmilojevic/vesnik",
		"https://github.com/acme/app.git":           "acme/app",
		"ssh://git@github.com/owner/repo.with.dots": "owner/repo.with.dots",
	} {
		tr := gittest.New(t)
		tr.Git("remote", "add", "origin", url)
		owner, name, err := tr.GitHubRepo(ctx, "origin")
		if err != nil || owner+"/"+name != want {
			t.Errorf("%s: got %s/%s, %v", url, owner, name, err)
		}
	}
	tr := gittest.New(t)
	tr.Git("remote", "add", "origin", "https://gitlab.com/a/b.git")
	if _, _, err := tr.GitHubRepo(ctx, "origin"); err == nil {
		t.Fatal("want error for a non-GitHub remote")
	}
}
