package collect

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/nklmilojevic/vesnik/internal/forge/github"
	"github.com/nklmilojevic/vesnik/internal/gitx/gittest"
)

type fakePRs struct {
	mu    sync.Mutex
	bySHA map[string][]github.PR
	calls int
}

func (f *fakePRs) PullsForCommit(_ context.Context, sha string) ([]github.PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.bySHA[sha], nil
}

func TestCollectScopesSkipsReleaseChoresAndDedupesPRs(t *testing.T) {
	ctx := context.Background()
	tr := gittest.New(t)
	tr.CommitFile("README.md", "x", "chore: init")
	tr.Git("tag", "api@v1.0.0")
	a := tr.CommitFile("api/a.php", "1", "feat(api): users endpoint")
	b := tr.CommitFile("api/a.php", "2", "fix(api): users pagination")
	tr.CommitFile("web/x.ts", "1", "feat(web): unrelated")
	tr.CommitFile("api/composer.json", "{}", "chore(release): api@v1.1.0")

	pr := github.PR{Number: 12, Title: "Users endpoint"}
	src := &fakePRs{bySHA: map[string][]github.PR{a: {pr}, b: {pr}}}
	set, err := Collect(ctx, tr.Repo, src, "api", "api@v1.0.0", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Changes) != 2 {
		t.Fatalf("changes = %+v", set.Changes)
	}
	if set.Changes[0].Commit.Subject != "feat(api): users endpoint" || set.Changes[1].Commit.Subject != "fix(api): users pagination" {
		t.Fatalf("order = %+v", set.Changes)
	}
	if prs := set.PRs(); len(prs) != 1 || prs[0].Number != 12 {
		t.Fatalf("PRs = %+v", prs)
	}
	if src.calls != 2 {
		t.Fatalf("PR lookups = %d", src.calls)
	}
	if msgs := set.Messages(); len(msgs) != 2 {
		t.Fatalf("messages = %v", msgs)
	}
}

type rateLimitedPRs struct {
	mu    sync.Mutex
	calls int
}

func (f *rateLimitedPRs) PullsForCommit(context.Context, string) ([]github.PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return nil, &github.Error{Status: 403, Message: "API rate limit exceeded for user ID 1."}
}

func TestCollectStopsLookupsWhenRateLimited(t *testing.T) {
	tr := gittest.New(t)
	for i := range 30 {
		tr.CommitFile("a", strconv.Itoa(i), "fix: change "+strconv.Itoa(i))
	}
	src := &rateLimitedPRs{}
	set, err := Collect(context.Background(), tr.Repo, src, "", "", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Only the lookups already in flight when the first refusal arrived.
	if src.calls > 8 || !set.RateLimited || set.MissingPRs != 30 {
		t.Fatalf("calls = %d, set = %+v", src.calls, set)
	}
}

func TestCollectWholeHistoryWithoutSource(t *testing.T) {
	tr := gittest.New(t)
	tr.CommitFile("a", "1", "feat: first")
	tr.CommitFile("a", "2", "fix: second")
	set, err := Collect(context.Background(), tr.Repo, nil, ".", "", "HEAD")
	if err != nil || len(set.Changes) != 2 {
		t.Fatalf("set = %+v, %v", set, err)
	}
}

func TestIsReleaseCommit(t *testing.T) {
	for subject, want := range map[string]bool{
		"chore(release): v0.31.12":                                           true,
		"chore(release): 0.0.65":                                             true,
		"chore(release): api@v1.2.0, web@v2.0.0-rc.1":                        true,
		"chore(release): cross-compile x86_64-apple-darwin on Apple silicon": false,
		"chore(release): bump goreleaser to v2.18.3":                         false,
		"chore: release v1.0.0":                                              false,
	} {
		if got := IsReleaseCommit(subject); got != want {
			t.Errorf("IsReleaseCommit(%q) = %v, want %v", subject, got, want)
		}
	}
}

type failingPRs struct{}

func (failingPRs) PullsForCommit(context.Context, string) ([]github.PR, error) {
	return nil, errors.New("dial tcp: i/o timeout")
}

func TestCollectSurvivesFailedPRLookups(t *testing.T) {
	tr := gittest.New(t)
	tr.CommitFile("a", "1", "feat: first")
	tr.CommitFile("a", "2", "fix: second")
	set, err := Collect(context.Background(), tr.Repo, failingPRs{}, ".", "", "HEAD")
	if err != nil || len(set.Changes) != 2 || set.MissingPRs != 2 {
		t.Fatalf("set = %+v, %v", set, err)
	}
}
