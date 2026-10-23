// Package collect gathers what changed in a package since its last tag: the
// commits that touch its path and the merged pull requests behind them.
package collect

import (
	"context"
	"regexp"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/nklmilojevic/vesnik/internal/forge/github"
	"github.com/nklmilojevic/vesnik/internal/gitx"
)

// PRSource finds the pull requests behind a commit.
type PRSource interface {
	PullsForCommit(ctx context.Context, sha string) ([]github.PR, error)
}

// StopOnRateLimit returns a PRSource that, once src is refused for GitHub's
// rate limit, refuses every later lookup without sending it. Share one
// across the collections of a release so the stop holds for every package.
func StopOnRateLimit(src PRSource) PRSource {
	if src == nil {
		return nil
	}
	if s, ok := src.(*stopper); ok {
		return s
	}
	return &stopper{src: src}
}

type stopper struct {
	src PRSource
	err atomic.Pointer[error]
}

func (s *stopper) PullsForCommit(ctx context.Context, sha string) ([]github.PR, error) {
	if err := s.err.Load(); err != nil {
		return nil, *err
	}
	prs, err := s.src.PullsForCommit(ctx, sha)
	if github.RateLimited(err) {
		s.err.CompareAndSwap(nil, &err)
	}
	return prs, err
}

// Change is one commit with its pull requests.
type Change struct {
	Commit gitx.Commit
	PRs    []github.PR
}

// Set is everything that changed in a package between two refs.
type Set struct {
	From    string
	To      string
	Changes []Change
	// MissingPRs counts commits whose pull request lookup failed; their
	// notes rely on the commit message alone.
	MissingPRs int
	// RateLimited means GitHub refused lookups for its rate limit, so the
	// remaining commits were not looked up.
	RateLimited bool
}

// Messages returns every commit message, for the conventional-commit floor.
func (s Set) Messages() []string {
	out := make([]string, len(s.Changes))
	for i, c := range s.Changes {
		out[i] = c.Commit.Message()
	}
	return out
}

// PRs returns each pull request once, in commit order.
func (s Set) PRs() []github.PR {
	seen := map[int]bool{}
	var out []github.PR
	for _, c := range s.Changes {
		for _, pr := range c.PRs {
			if !seen[pr.Number] {
				seen[pr.Number] = true
				out = append(out, pr)
			}
		}
	}
	return out
}

// releaseCommit matches the commits vesnik and similar tools make to record
// a release: "chore(release): " and nothing but version tags.
var releaseCommit = regexp.MustCompile(`^chore\(release\): \S*\d+\.\d+\.\d+\S*(, \S*\d+\.\d+\.\d+\S*)*$`)

// IsReleaseCommit reports whether a subject only records a release. Other
// commits in the release scope, such as build changes, still count.
func IsReleaseCommit(subject string) bool {
	return releaseCommit.MatchString(subject)
}

// Collect returns the changes in from..to that touch path. An empty from
// means the whole history. src may be nil to skip pull request lookups.
func Collect(ctx context.Context, repo gitx.Repo, src PRSource, path, from, to string) (Set, error) {
	var paths []string
	if path != "" && path != "." {
		paths = []string{path}
	}
	commits, err := repo.Log(ctx, from, to, paths...)
	if err != nil {
		return Set{}, err
	}
	set := Set{From: from, To: to}
	for _, c := range commits {
		if !IsReleaseCommit(c.Subject) {
			set.Changes = append(set.Changes, Change{Commit: c})
		}
	}
	if src == nil {
		return set, nil
	}
	// Every further request would fail too and keep the limit spent.
	src = StopOnRateLimit(src)
	// Pull requests only add context, so a failed lookup is counted, not fatal.
	var g errgroup.Group
	g.SetLimit(8)
	var missing atomic.Int64
	var limited atomic.Bool
	for i := range set.Changes {
		g.Go(func() error {
			prs, err := src.PullsForCommit(ctx, set.Changes[i].Commit.Hash)
			if err != nil {
				if github.RateLimited(err) {
					limited.Store(true)
				}
				missing.Add(1)
				return nil
			}
			set.Changes[i].PRs = prs
			return nil
		})
	}
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		return Set{}, err
	}
	set.MissingPRs = int(missing.Load())
	set.RateLimited = limited.Load()
	return set, nil
}
