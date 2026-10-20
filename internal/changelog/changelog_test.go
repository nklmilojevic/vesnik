package changelog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDemoteSkipsCodeBlocks(t *testing.T) {
	in := "Intro.\n\n## Fixes\n\n- A fix.\n\n```sh\n# a comment\n```\n\n### Deep\n#hashtag"
	want := "Intro.\n\n### Fixes\n\n- A fix.\n\n```sh\n# a comment\n```\n\n#### Deep\n#hashtag"
	if got := Demote(in); got != want {
		t.Fatalf("Demote =\n%s", got)
	}
}

func TestPrependCreatesAndStacks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := Prepend(path, Section("v1.0.0", "2026-10-20", "First.\n\n## Fixes\n\n- A.")); err != nil {
		t.Fatal(err)
	}
	if err := Prepend(path, Section("v1.1.0", "2026-10-21", "Second.")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "# Changelog\n\n## v1.1.0 - 2026-10-21\n\nSecond.\n\n## v1.0.0 - 2026-10-20\n\nFirst.\n\n### Fixes\n\n- A.\n"
	if string(got) != want {
		t.Fatalf("CHANGELOG.md =\n%s", got)
	}
}

func TestPrependKeepsIntroAndHandlesNoTitle(t *testing.T) {
	dir := t.TempDir()
	intro := filepath.Join(dir, "intro.md")
	_ = os.WriteFile(intro, []byte("# Changes\n\nAll notable changes.\n\n## 1.0.0\n\nOld.\n"), 0o644)
	if err := Prepend(intro, Section("1.1.0", "d", "New.")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(intro)
	if string(got) != "# Changes\n\nAll notable changes.\n\n## 1.1.0 - d\n\nNew.\n\n## 1.0.0\n\nOld.\n" {
		t.Fatalf("with intro =\n%s", got)
	}

	bare := filepath.Join(dir, "bare.md")
	_ = os.WriteFile(bare, []byte("## 1.0.0\n\nOld.\n"), 0o644)
	if err := Prepend(bare, Section("1.1.0", "d", "New.")); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(bare)
	if string(got) != "# Changelog\n\n## 1.1.0 - d\n\nNew.\n\n## 1.0.0\n\nOld.\n" {
		t.Fatalf("without title =\n%s", got)
	}

	titleOnly := filepath.Join(dir, "title.md")
	_ = os.WriteFile(titleOnly, []byte("# Changelog\n"), 0o644)
	if err := Prepend(titleOnly, Section("1.0.0", "d", "First.")); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(titleOnly)
	if string(got) != "# Changelog\n\n## 1.0.0 - d\n\nFirst.\n" {
		t.Fatalf("title only =\n%s", got)
	}
}
