// Package gitx runs git. vesnik shells out instead of using a git library so
// the user's own configuration applies: tag.gpgSign, SSH signing, hooks,
// credential helpers and remotes.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Repo is a git work tree.
type Repo struct {
	Dir string
}

// Commit is one commit in a range.
type Commit struct {
	Hash    string
	Subject string
	Body    string
}

// Message returns the subject and body joined as git stores them.
func (c Commit) Message() string {
	if c.Body == "" {
		return c.Subject
	}
	return c.Subject + "\n\n" + c.Body
}

// Run runs git with args in the repo and returns trimmed stdout.
func (r Repo) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Open finds the top level of the work tree that contains dir.
func Open(ctx context.Context, dir string) (Repo, error) {
	top, err := Repo{Dir: dir}.Run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return Repo{}, fmt.Errorf("not a git repository: %w", err)
	}
	return Repo{Dir: top}, nil
}

// Head returns the commit HEAD points to.
func (r Repo) Head(ctx context.Context) (string, error) {
	return r.Run(ctx, "rev-parse", "HEAD")
}

// Branch returns the current branch, or an error on a detached HEAD.
func (r Repo) Branch(ctx context.Context) (string, error) {
	b, err := r.Run(ctx, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	if b == "" {
		return "", errors.New("HEAD is detached; check out the release branch")
	}
	return b, nil
}

// DefaultBranch resolves origin's default branch from refs/remotes/origin/HEAD.
func (r Repo) DefaultBranch(ctx context.Context) (string, error) {
	ref, err := r.Run(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("cannot resolve origin's default branch "+
			"(run `git remote set-head origin --auto` or set `branch` in .vesnik.yaml): %w", err)
	}
	return strings.TrimPrefix(ref, "origin/"), nil
}

// Clean reports whether tracked files have no changes.
func (r Repo) Clean(ctx context.Context) (bool, error) {
	out, err := r.Run(ctx, "status", "--porcelain", "--untracked-files=no")
	return out == "", err
}

// Tags lists tags matching a glob, or every tag for an empty glob.
func (r Repo) Tags(ctx context.Context, glob string) ([]string, error) {
	args := []string{"tag", "--list"}
	if glob != "" {
		args = append(args, glob)
	}
	out, err := r.Run(ctx, args...)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// RefExists reports whether ref resolves to a commit.
func (r Repo) RefExists(ctx context.Context, ref string) bool {
	_, err := r.Run(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// Log returns non-merge commits in from..to, oldest first, that touch any of
// paths. An empty from means the whole history up to to.
func (r Repo) Log(ctx context.Context, from, to string, paths ...string) ([]Commit, error) {
	rng := to
	if from != "" {
		rng = from + ".." + to
	}
	args := []string{"log", "--no-merges", "--reverse", "--format=%H" + fieldSep + "%s" + fieldSep + "%b" + recordSep, rng}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	out, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for rec := range strings.SplitSeq(out, recordSep) {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, fieldSep, 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected git log record %q", rec)
		}
		commits = append(commits, Commit{Hash: parts[0], Subject: parts[1], Body: strings.TrimSpace(parts[2])})
	}
	return commits, nil
}

// Add stages paths.
func (r Repo) Add(ctx context.Context, paths ...string) error {
	_, err := r.Run(ctx, append([]string{"add", "--"}, paths...)...)
	return err
}

// AddUpdated stages changes to tracked files.
func (r Repo) AddUpdated(ctx context.Context) error {
	_, err := r.Run(ctx, "add", "--update")
	return err
}

// Staged reports whether the index differs from HEAD.
func (r Repo) Staged(ctx context.Context) (bool, error) {
	out, err := r.Run(ctx, "diff", "--cached", "--name-only")
	return out != "", err
}

// Commit records the index with msg.
func (r Repo) Commit(ctx context.Context, msg string) error {
	_, err := r.Run(ctx, "commit", "-m", msg)
	return err
}

// Tag creates an annotated tag at HEAD. Annotated tags follow tag.gpgSign.
func (r Repo) Tag(ctx context.Context, name, msg string) error {
	_, err := r.Run(ctx, "tag", "-a", name, "-m", msg)
	return err
}

// DeleteTag removes a local tag.
func (r Repo) DeleteTag(ctx context.Context, name string) error {
	_, err := r.Run(ctx, "tag", "-d", name)
	return err
}

// ResetHard moves the branch and work tree back to rev.
func (r Repo) ResetHard(ctx context.Context, rev string) error {
	_, err := r.Run(ctx, "reset", "--hard", rev)
	return err
}

// Fetch updates remote refs and tags for branch.
func (r Repo) Fetch(ctx context.Context, remote, branch string) error {
	_, err := r.Run(ctx, "fetch", "--tags", remote, branch)
	return err
}

// PullFF fast-forwards the current branch from remote.
func (r Repo) PullFF(ctx context.Context, remote, branch string) error {
	_, err := r.Run(ctx, "merge", "--ff-only", remote+"/"+branch)
	return err
}

// Push pushes the branch and tags atomically.
func (r Repo) Push(ctx context.Context, remote, branch string, tags ...string) error {
	args := make([]string, 0, 4+len(tags))
	args = append(args, "push", "--atomic", remote, branch)
	for _, t := range tags {
		args = append(args, "refs/tags/"+t)
	}
	_, err := r.Run(ctx, args...)
	return err
}

var remoteRe = regexp.MustCompile(`github\.com[:/]([^/]+)/([^/]+?)(?:\.git)?/?$`)

// GitHubRepo returns owner and name of the remote's GitHub repository.
func (r Repo) GitHubRepo(ctx context.Context, remote string) (string, string, error) {
	url, err := r.Run(ctx, "remote", "get-url", remote)
	if err != nil {
		return "", "", err
	}
	m := remoteRe.FindStringSubmatch(url)
	if m == nil {
		return "", "", fmt.Errorf("remote %s (%s) is not a GitHub repository", remote, url)
	}
	return m[1], m[2], nil
}
