package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/gitx/gittest"
)

func laravel(t *testing.T) *gittest.TestRepo {
	t.Helper()
	tr := gittest.New(t)
	tr.Write("README.md", "# App\n\nThe email marketing app.\n")
	tr.Write("composer.json", `{"name": "acme/app", "type": "project"}`)
	tr.Write("package.json", `{"private": true}`)
	tr.Write("config/app.php", "<?php\nreturn [\n    'version' => '1.4.2',\n];\n")
	tr.Write("justfile", "build:\n    bun run build\n\nrelease:\n    git merge develop\n    gh release create $VERSION --target main\n")
	tr.Write(".github/workflows/prod.yml", "name: Prod\non:\n  push:\n    tags: [\"v*\"]\njobs:\n  deploy:\n    runs-on: ubuntu-latest\n")
	tr.Write(".env", "SECRET=do-not-read\nversion = '9.9.9'\n")
	tr.Git("add", "README.md", "composer.json", "package.json", "config", "justfile", ".github")
	tr.Git("commit", "-q", "-m", "ABC-1: init")
	tr.Git("tag", "v1.4.2")
	return tr
}

func TestGatherReadsTrackedContextOnly(t *testing.T) {
	tr := laravel(t)
	pkgs, err := Detect(tr.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].Ecosystem != "php" {
		t.Fatalf("packages = %+v", pkgs)
	}
	c := Gather(context.Background(), tr.Repo, pkgs)
	if c.TagCount != 1 || c.RecentTags[0] != "v1.4.2" || c.Subjects[0] != "ABC-1: init" {
		t.Fatalf("context = %+v", c)
	}
	if !strings.Contains(c.Files["justfile"], "gh release create") || strings.Contains(c.Files["justfile"], "build:") {
		t.Fatalf("justfile = %q", c.Files["justfile"])
	}
	if !strings.Contains(c.Files[".github/workflows/prod.yml"], "tags:") || strings.Contains(c.Files[".github/workflows/prod.yml"], "runs-on") {
		t.Fatalf("workflow = %q", c.Files[".github/workflows/prod.yml"])
	}
	if len(c.VersionLines) != 1 || !strings.HasPrefix(c.VersionLines[0], "config/app.php:3:") {
		t.Fatalf("version lines = %v", c.VersionLines)
	}
	if prompt := c.Prompt(); strings.Contains(prompt, "do-not-read") || strings.Contains(prompt, "9.9.9") {
		t.Fatal("an untracked .env reached the prompt")
	}
}

func TestApplyKeepsOnlyWhatHolds(t *testing.T) {
	tr := laravel(t)
	pkgs, _ := Detect(tr.Dir)
	c := Context{DefaultBranch: "develop", Branches: []string{"develop", "main"}}
	set := Defaults(pkgs)
	dropped := Apply(tr.Dir, c, Suggestion{
		Product: " Acme's main app; notes are for engineers. ",
		Preset:  "pirate",
		Rules:   []string{"Keep ticket keys such as ABC-123.", " ", "Leave out bookkeeping commits."},
		Branch:  "main",
		Files: []FileRule{
			{File: "config/app.php", Regex: `'version' => '(?P<v>[^']+)'`},
			{File: "config/app.php", Regex: `'build' => '([^']+)'`},
			{File: "../outside", Regex: `(x)`},
			{File: "config/app.php", Regex: `version`},
		},
		Summary: "Releases tag main.",
	}, &set)
	if set.Preset != "ste100" || set.Branch != "main" || set.Changelog || len(set.Rules) != 2 {
		t.Fatalf("settings = %+v", set)
	}
	if len(set.Packages[0].Files) != 1 || len(dropped) != 3 {
		t.Fatalf("files = %+v, dropped = %v", set.Packages[0].Files, dropped)
	}

	set = Defaults(pkgs)
	if d := Apply(tr.Dir, c, Suggestion{Branch: "release"}, &set); len(d) != 1 || set.Branch != "" {
		t.Fatalf("unknown branch: %v, %+v", d, set)
	}
}

func TestRenderLoads(t *testing.T) {
	tr := laravel(t)
	pkgs, _ := Detect(tr.Dir)
	set := Defaults(pkgs)
	set.Product = `The "main" app`
	set.Rules = []string{`Keep keys like "ABC-123".`}
	set.Branch = "main"
	set.Changelog = false
	set.Summary = strings.Repeat("A long summary line. ", 10)
	set.Packages[0].Files = []config.FileRule{{File: "config/app.php", Regex: `'version' => '(?P<v>[^']+)'`}}
	if err := os.WriteFile(filepath.Join(tr.Dir, config.FileName), []byte(Render(set)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(tr.Dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, Render(set))
	}
	if cfg.Style.Product != `The "main" app` || cfg.Style.Rules[0] != `Keep keys like "ABC-123".` || cfg.Branch != "main" || *cfg.Changelog.Enabled {
		t.Fatalf("cfg = %+v", cfg)
	}
	if f := cfg.Packages[0].Files; len(f) != 1 || f[0].Regex != `'version' => '(?P<v>[^']+)'` {
		t.Fatalf("files = %+v", f)
	}

	set.Packages = []config.Package{{Name: "a", Path: "x", Ecosystem: "go"}, {Name: "b", Path: "y", Ecosystem: "node"}}
	if out := Render(set); !strings.Contains(out, "  - name: a\n    path: x\n    ecosystem: go\n") {
		t.Fatalf("monorepo render:\n%s", out)
	}
}

func TestGatherClipsMinifiedLines(t *testing.T) {
	tr := gittest.New(t)
	tr.CommitFile("public/app.json", `{"version":"1.2.3","blob":"`+strings.Repeat("x", 100_000)+`"}`, "init")
	c := Gather(context.Background(), tr.Repo, []config.Package{{Name: "x", Path: "."}})
	if len(c.VersionLines) != 1 || len(c.VersionLines[0]) > 210 {
		t.Fatalf("version line not clipped: %d chars", len(c.VersionLines[0]))
	}
	c.Files["huge"] = strings.Repeat("y", 500_000)
	if n := len(c.Prompt()); n > promptLimit+10 {
		t.Fatalf("prompt = %d chars", n)
	}
}
