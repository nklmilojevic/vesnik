// Package gittest builds throwaway git repositories for tests.
package gittest

import (
	"context"

	"os"
	"path/filepath"
	"testing"

	"github.com/nklmilojevic/vesnik/internal/gitx"
)

// TestRepo is a throwaway repository for tests in any package.
type TestRepo struct {
	gitx.Repo
	t *testing.T
}

// repoEnv points git at a repository other than the working directory. Git
// sets these for hooks, so tests run from a pre-push hook would otherwise
// init, configure and commit in the developer's repository.
var repoEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
}

// New creates an empty repository with a fixed identity and signing
// turned off, so tests never depend on the developer's git config or on
// the repository a hook runs them in.
func New(t *testing.T) *TestRepo {
	t.Helper()
	for _, k := range repoEnv {
		if v, ok := os.LookupEnv(k); ok {
			t.Setenv(k, v) // restores the value after the test
			if err := os.Unsetenv(k); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	tr := &TestRepo{Repo: gitx.Repo{Dir: t.TempDir()}, t: t}
	tr.Git("init", "-q", "-b", "main")
	tr.Git("config", "user.name", "Test")
	tr.Git("config", "user.email", "test@example.com")
	tr.Git("config", "commit.gpgSign", "false")
	tr.Git("config", "tag.gpgSign", "false")
	return tr
}

// Git runs git and fails the test on error.
func (tr *TestRepo) Git(args ...string) string {
	tr.t.Helper()
	out, err := tr.Run(context.Background(), args...)
	if err != nil {
		tr.t.Fatal(err)
	}
	return out
}

// Write creates or replaces a file relative to the repo root.
func (tr *TestRepo) Write(path, content string) {
	tr.t.Helper()
	full := filepath.Join(tr.Dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		tr.t.Fatal(err)
	}
}

// Read returns a file's content relative to the repo root.
func (tr *TestRepo) Read(path string) string {
	tr.t.Helper()
	b, err := os.ReadFile(filepath.Join(tr.Dir, path))
	if err != nil {
		tr.t.Fatal(err)
	}
	return string(b)
}

// CommitFile writes a file and commits it with msg.
func (tr *TestRepo) CommitFile(path, content, msg string) string {
	tr.t.Helper()
	tr.Write(path, content)
	tr.Git("add", path)
	tr.Git("commit", "-q", "-m", msg)
	return tr.Git("rev-parse", "HEAD")
}
