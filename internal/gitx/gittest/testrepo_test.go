package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// outerGit runs git on the outer repository, without the hook variables
// this test may itself be running under.
func outerGit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains(repoEnv, k)
	})
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// A pre-push hook runs the tests with GIT_DIR and GIT_INDEX_FILE pointing
// at the developer's repository; New must not touch it.
func TestNewIgnoresHookRepoEnv(t *testing.T) {
	outer := t.TempDir()
	if out, err := outerGit(t, "init", "-q", outer); err != nil {
		t.Fatal(out)
	}
	gitDir := filepath.Join(outer, ".git")
	before, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", gitDir)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(gitDir, "index"))

	tr := New(t)
	tr.CommitFile("a.txt", "a\n", "feat: a")

	dir, err := filepath.EvalSymlinks(tr.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(tr.Git("rev-parse", "--absolute-git-dir")); !strings.HasPrefix(got, dir) {
		t.Fatalf("test repo uses git dir %s, want one under %s", got, dir)
	}
	after, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("outer repo config changed:\n%s", after)
	}
	if out, err := outerGit(t, "--git-dir", gitDir, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		t.Fatalf("outer repo got a commit: %s", out)
	}
}
