// Package github is the small slice of the GitHub REST API vesnik needs:
// pull requests for a commit, generated release notes, and releases.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Client talks to one repository.
type Client struct {
	HTTP  *http.Client
	Base  string
	Token string
	Owner string
	Repo  string
}

// New returns a client for owner/repo. The token comes from GH_TOKEN,
// GITHUB_TOKEN, or `gh auth token`, in that order.
func New(ctx context.Context, owner, repo string) (*Client, error) {
	token, err := Token(ctx)
	if err != nil {
		return nil, err
	}
	base := "https://api.github.com"
	if v := os.Getenv("VESNIK_GITHUB_API"); v != "" {
		base = strings.TrimRight(v, "/")
	}
	return &Client{HTTP: &http.Client{Timeout: 60 * time.Second}, Base: base, Token: token, Owner: owner, Repo: repo}, nil
}

// Token resolves a GitHub token.
func Token(ctx context.Context) (string, error) {
	for _, env := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v, nil
		}
	}
	if _, err := exec.LookPath("gh"); err == nil {
		out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return "", errors.New("no GitHub token: set GH_TOKEN or GITHUB_TOKEN, or run `gh auth login`")
}

// Error is a non-2xx response.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("GitHub API %d: %s", e.Status, e.Message) }

// RateLimited reports whether GitHub refused a request for its primary or
// secondary rate limit.
func RateLimited(err error) bool {
	apiErr, ok := errors.AsType[*Error](err)
	if !ok {
		return false
	}
	return apiErr.Status == http.StatusTooManyRequests ||
		apiErr.Status == http.StatusForbidden && strings.Contains(strings.ToLower(apiErr.Message), "rate limit")
}

// retries is how many times a request that is safe to repeat is sent before
// a transient failure is returned.
const retries = 3

// Backoff is the wait before the second attempt; it grows linearly.
var Backoff = 500 * time.Millisecond

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = b
	}
	// Creating a release is the one call that is not safe to repeat.
	attempts := retries
	if method == http.MethodPost && !strings.HasSuffix(path, "/generate-notes") {
		attempts = 1
	}
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err = c.once(ctx, method, path, payload, out); err == nil || !transient(ctx, err) || attempt == attempts {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * Backoff):
		}
	}
	return err
}

// transient reports whether a failure is worth retrying: a network error or
// a gateway error, but not a cancelled context or a 4xx.
func transient(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if apiErr, ok := errors.AsType[*Error](err); ok {
		return apiErr.Status == http.StatusBadGateway || apiErr.Status == http.StatusServiceUnavailable ||
			apiErr.Status == http.StatusGatewayTimeout
	}
	return true
}

func (c *Client) once(ctx context.Context, method, path string, payload []byte, out any) error {
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(data))
		}
		return &Error{Status: resp.StatusCode, Message: e.Message}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *Client) repoPath(format string, args ...any) string {
	return fmt.Sprintf("/repos/%s/%s", url.PathEscape(c.Owner), url.PathEscape(c.Repo)) + fmt.Sprintf(format, args...)
}

// PR is a merged pull request with the fields the notes prompt uses.
type PR struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"-"`
	URL    string   `json:"html_url"`
	Merged bool     `json:"-"`
}

// PullsForCommit returns the merged pull requests that contain sha.
func (c *Client) PullsForCommit(ctx context.Context, sha string) ([]PR, error) {
	var raw []struct {
		PR
		MergedAt *string `json:"merged_at"`
		Labels   []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := c.do(ctx, http.MethodGet, c.repoPath("/commits/%s/pulls", sha), nil, &raw); err != nil {
		return nil, err
	}
	var prs []PR
	for _, r := range raw {
		if r.MergedAt == nil {
			continue
		}
		pr := r.PR
		pr.Merged = true
		for _, l := range r.Labels {
			pr.Labels = append(pr.Labels, l.Name)
		}
		prs = append(prs, pr)
	}
	return prs, nil
}

// GenerateNotes returns GitHub's generated notes (the PR list and new
// contributors) for tag, compared to previous when it is not empty.
func (c *Client) GenerateNotes(ctx context.Context, tag, target, previous string) (string, error) {
	body := map[string]string{"tag_name": tag}
	if target != "" {
		body["target_commitish"] = target
	}
	if previous != "" {
		body["previous_tag_name"] = previous
	}
	var out struct {
		Body string `json:"body"`
	}
	if err := c.do(ctx, http.MethodPost, c.repoPath("/releases/generate-notes"), body, &out); err != nil {
		return "", err
	}
	return out.Body, nil
}

// Release is a GitHub Release.
type Release struct {
	ID         int64  `json:"id,omitempty"`
	TagName    string `json:"tag_name"`
	Target     string `json:"target_commitish,omitempty"`
	Name       string `json:"name,omitempty"`
	Body       string `json:"body"`
	Prerelease bool   `json:"prerelease"`
	MakeLatest string `json:"make_latest,omitempty"`
	URL        string `json:"html_url,omitempty"`
}

// CreateRelease publishes a release for an existing tag.
func (c *Client) CreateRelease(ctx context.Context, r Release) (Release, error) {
	var out Release
	err := c.do(ctx, http.MethodPost, c.repoPath("/releases"), r, &out)
	return out, err
}

// ReleaseByTag finds the release of a tag.
func (c *Client) ReleaseByTag(ctx context.Context, tag string) (Release, error) {
	var out Release
	err := c.do(ctx, http.MethodGet, c.repoPath("/releases/tags/%s", url.PathEscape(tag)), nil, &out)
	return out, err
}

// UpdateReleaseBody replaces a release's description.
func (c *Client) UpdateReleaseBody(ctx context.Context, id int64, body string) (Release, error) {
	var out Release
	err := c.do(ctx, http.MethodPatch, c.repoPath("/releases/%d", id), map[string]string{"body": body}, &out)
	return out, err
}
