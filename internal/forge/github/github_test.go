package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), Base: srv.URL, Token: "tok", Owner: "o", Repo: "r"}
}

func TestPullsForCommitKeepsMergedWithLabels(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/commits/abc/pulls" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("unexpected request %s %v", r.URL.Path, r.Header)
		}
		_, _ = io.WriteString(w, `[
			{"number": 1, "title": "Add x", "body": "why", "merged_at": "2026-01-01T00:00:00Z", "labels": [{"name": "feature"}]},
			{"number": 2, "title": "Open", "merged_at": null}
		]`)
	})
	prs, err := c.PullsForCommit(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 1 || prs[0].Body != "why" || len(prs[0].Labels) != 1 || prs[0].Labels[0] != "feature" {
		t.Fatalf("prs = %+v", prs)
	}
}

func TestGenerateNotesSendsPreviousTag(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method != http.MethodPost || body["tag_name"] != "v1.1.0" || body["previous_tag_name"] != "v1.0.0" || body["target_commitish"] != "sha" {
			t.Errorf("unexpected request %s %v", r.Method, body)
		}
		_, _ = io.WriteString(w, `{"name": "v1.1.0", "body": "## What's Changed\n* x"}`)
	})
	got, err := c.GenerateNotes(context.Background(), "v1.1.0", "sha", "v1.0.0")
	if err != nil || got != "## What's Changed\n* x" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReleaseLifecycle(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /repos/o/r/releases":
			var rel Release
			_ = json.NewDecoder(r.Body).Decode(&rel)
			if !rel.Prerelease || rel.TagName != "v1.0.0-rc.1" {
				t.Errorf("release = %+v", rel)
			}
			_, _ = io.WriteString(w, `{"id": 7, "tag_name": "v1.0.0-rc.1", "html_url": "https://x"}`)
		case "GET /repos/o/r/releases/tags/api@v1.0.0":
			_, _ = io.WriteString(w, `{"id": 9, "tag_name": "api@v1.0.0", "body": "old"}`)
		case "PATCH /repos/o/r/releases/9":
			_, _ = io.WriteString(w, `{"id": 9, "body": "new"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message": "Not Found"}`)
		}
	})
	ctx := context.Background()
	rel, err := c.CreateRelease(ctx, Release{TagName: "v1.0.0-rc.1", Prerelease: true})
	if err != nil || rel.ID != 7 || rel.URL != "https://x" {
		t.Fatalf("CreateRelease = %+v, %v", rel, err)
	}
	rel, err = c.ReleaseByTag(ctx, "api@v1.0.0")
	if err != nil || rel.ID != 9 {
		t.Fatalf("ReleaseByTag = %+v, %v", rel, err)
	}
	if rel, err = c.UpdateReleaseBody(ctx, 9, "new"); err != nil || rel.Body != "new" {
		t.Fatalf("UpdateReleaseBody = %+v, %v", rel, err)
	}
	_, err = c.ReleaseByTag(ctx, "missing")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 404 || apiErr.Message != "Not Found" {
		t.Fatalf("err = %v", err)
	}
}

func TestTokenFromEnv(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", " from-actions ")
	got, err := Token(context.Background())
	if err != nil || got != "from-actions" {
		t.Fatalf("Token = %q, %v", got, err)
	}
}

func TestRetriesTransientFailuresButNotReleaseCreation(t *testing.T) {
	Backoff = time.Millisecond
	calls := map[string]int{}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		calls[key]++
		switch {
		case key == "GET /repos/o/r/commits/abc/pulls" && calls[key] < 3:
			w.WriteHeader(http.StatusBadGateway)
		case key == "GET /repos/o/r/commits/abc/pulls":
			_, _ = io.WriteString(w, `[]`)
		case key == "POST /repos/o/r/releases":
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message": "Not Found"}`)
		}
	})
	ctx := context.Background()
	if _, err := c.PullsForCommit(ctx, "abc"); err != nil {
		t.Fatalf("GET after two 502s: %v", err)
	}
	if _, err := c.CreateRelease(ctx, Release{TagName: "v1.0.0"}); err == nil {
		t.Fatal("want the 502 from release creation")
	}
	if _, err := c.ReleaseByTag(ctx, "missing"); err == nil {
		t.Fatal("want 404")
	}
	if calls["GET /repos/o/r/commits/abc/pulls"] != 3 || calls["POST /repos/o/r/releases"] != 1 || calls["GET /repos/o/r/releases/tags/missing"] != 1 {
		t.Fatalf("calls = %v", calls)
	}
}
