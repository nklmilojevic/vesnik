package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"

	"github.com/nklmilojevic/vesnik/internal/collect"
	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/forge/github"
	"github.com/nklmilojevic/vesnik/internal/gitx"
	"github.com/nklmilojevic/vesnik/internal/version"
)

func TestSystemPresets(t *testing.T) {
	in := Input{Project: "sofka", Current: semver.MustParse("0.31.13")}
	ste, err := System("", config.Style{Preset: "ste100", Product: "a terminal UI for Kubernetes", Rules: []string{"Call pods pods."}}, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"You write release notes for sofka, a terminal UI for Kubernetes.",
		"ASD-STE100",
		"- Call pods pods.",
		"'## New features', '## Fixes', '## Other changes'",
		"below 1.0.0",
	} {
		if !strings.Contains(ste, want) {
			t.Errorf("ste100 prompt lacks %q:\n%s", want, ste)
		}
	}
	kac, err := System("", config.Style{Preset: "keepachangelog"}, Input{Project: "x", Current: semver.MustParse("2.0.0")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kac, "'## Added', '## Changed'") || strings.Contains(kac, "below 1.0.0") {
		t.Fatalf("keepachangelog prompt:\n%s", kac)
	}
	custom, err := System("", config.Style{Preset: "casual", Sections: []string{"Highlights"}}, in)
	if err != nil || !strings.Contains(custom, "'## Highlights'.") {
		t.Fatalf("sections override: %v\n%s", err, custom)
	}
}

func TestSystemPromptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.tmpl"), []byte("Notes for {{.Project}} in {{index .Sections 0}}."), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := System(dir, config.Style{Preset: "ste100", PromptFile: "notes.tmpl"}, Input{Project: "app"})
	if err != nil || got != "Notes for app in New features." {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := System(dir, config.Style{PromptFile: "missing"}, Input{}); err == nil {
		t.Fatal("want error for a missing prompt file")
	}
}

func TestUser(t *testing.T) {
	pr := github.PR{Number: 3, Title: "Add matrix view", Body: strings.Repeat("x", 5000), Labels: []string{"feature"}}
	in := Input{
		Project: "app",
		Package: config.Package{Name: "api", Path: "packages/api"},
		Multi:   true,
		Current: semver.MustParse("1.2.3"),
		LastTag: "api@v1.2.3",
		Floor:   version.Minor,
		Changes: collect.Set{Changes: []collect.Change{
			{Commit: gitx.Commit{Subject: "feat: matrix", Body: "Shows verbs."}, PRs: []github.PR{pr}},
			{Commit: gitx.Commit{Subject: "fix: typo"}},
		}},
	}
	got := User(in)
	for _, want := range []string{
		"package api (path packages/api)",
		"current version is 1.2.3 (tag api@v1.2.3)",
		"at least a minor bump",
		"### feat: matrix\n\nShows verbs.\n\nPull request: Add matrix view",
		"### fix: typo",
		"Labels: feature",
		"[…]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("user prompt lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "x") > 4100 {
		t.Fatal("long PR body was not clipped")
	}
	first := User(Input{Project: "app", Floor: version.Patch})
	if !strings.Contains(first, "first release") {
		t.Fatalf("first release prompt:\n%s", first)
	}
}
