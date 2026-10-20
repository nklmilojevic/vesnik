package ecosystem

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nklmilojevic/vesnik/internal/config"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDetect(t *testing.T) {
	for file, want := range map[string]string{
		"Cargo.toml":    "cargo",
		"package.json":  "node",
		"composer.json": "php",
		"go.mod":        "go",
		"README.md":     "none",
	} {
		dir := t.TempDir()
		write(t, dir, file, "{}")
		b, err := For("auto", dir)
		if err != nil || b.Name() != want {
			t.Errorf("%s: got %v, %v; want %s", file, b, err, want)
		}
	}
	if _, err := For("maven", t.TempDir()); err == nil {
		t.Fatal("want error for unknown ecosystem")
	}
}

func TestNodeKeepsFormattingAndNestedVersions(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{
  "name": "web",
  "engines": { "version": "keep" },
    "version":   "1.2.3",
  "dependencies": {"a": "^1.2.3"}
}
`)
	write(t, dir, "package-lock.json", `{
  "name": "web",
  "version": "1.2.3",
  "lockfileVersion": 3,
  "packages": {
    "": {
      "name": "web",
      "version": "1.2.3"
    },
    "node_modules/a": {
      "version": "1.2.3"
    }
  }
}
`)
	if err := (Node{}).Write(context.Background(), dir, "1.3.0"); err != nil {
		t.Fatal(err)
	}
	want := `{
  "name": "web",
  "engines": { "version": "keep" },
    "version":   "1.3.0",
  "dependencies": {"a": "^1.2.3"}
}
`
	if got := read(t, dir, "package.json"); got != want {
		t.Fatalf("package.json =\n%s", got)
	}
	wantLock := `{
  "name": "web",
  "version": "1.3.0",
  "lockfileVersion": 3,
  "packages": {
    "": {
      "name": "web",
      "version": "1.3.0"
    },
    "node_modules/a": {
      "version": "1.2.3"
    }
  }
}
`
	if got := read(t, dir, "package-lock.json"); got != wantLock {
		t.Fatalf("package-lock.json =\n%s", got)
	}
	if files := (Node{}).Files(dir); len(files) != 2 {
		t.Fatalf("Files = %v", files)
	}
}

func TestNodeWithoutVersionIsTagOnly(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"name": "app", "private": true}`
	write(t, dir, "package.json", pkg)
	if files := (Node{}).Files(dir); len(files) != 0 {
		t.Fatalf("Files = %v", files)
	}
	if err := (Node{}).Write(context.Background(), dir, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if read(t, dir, "package.json") != pkg {
		t.Fatal("package.json changed")
	}
}

func TestDetectPrefersPHPAndGoOverNode(t *testing.T) {
	laravel := t.TempDir()
	write(t, laravel, "composer.json", "{}")
	write(t, laravel, "package.json", "{}")
	if b, _ := For("auto", laravel); b.Name() != "php" {
		t.Fatalf("Laravel app detected as %s", b.Name())
	}
	tool := t.TempDir()
	write(t, tool, "go.mod", "module x")
	write(t, tool, "package.json", "{}")
	if b, _ := For("auto", tool); b.Name() != "go" {
		t.Fatalf("Go tool detected as %s", b.Name())
	}
}

func TestPHPWithAndWithoutComposerVersion(t *testing.T) {
	ctx := context.Background()
	app := t.TempDir()
	composer := `{
    "name": "acme/app",
    "require": {"php": "^8.4"}
}
`
	write(t, app, "composer.json", composer)
	if files := (PHP{}).Files(app); len(files) != 0 {
		t.Fatalf("an app without a version field changes no files, got %v", files)
	}
	if err := (PHP{}).Write(ctx, app, "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, app, "composer.json"); got != composer {
		t.Fatalf("composer.json changed:\n%s", got)
	}

	lib := t.TempDir()
	write(t, lib, "composer.json", `{
    "name": "acme/lib",
    "version": "1.0.0"
}
`)
	if err := (PHP{}).Write(ctx, lib, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, lib, "composer.json"); got != "{\n    \"name\": \"acme/lib\",\n    \"version\": \"1.1.0\"\n}\n" {
		t.Fatalf("composer.json =\n%s", got)
	}
}

func TestCargoPackageAndWorkspace(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", `[package]
name = "demo"
version = "0.1.0" # keep this comment
edition = "2024"

[dependencies]
`)
	write(t, dir, "src/main.rs", "fn main() {}\n")
	cmd := exec.Command("cargo", "generate-lockfile", "--offline")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cargo cannot generate a lockfile here: %s", out)
	}
	if err := (Cargo{}).Write(ctx, dir, "0.2.0"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "Cargo.toml"); got != `[package]
name = "demo"
version = "0.2.0" # keep this comment
edition = "2024"

[dependencies]
` {
		t.Fatalf("Cargo.toml =\n%s", got)
	}
	if lock := read(t, dir, "Cargo.lock"); !strings.Contains(lock, "name = \"demo\"\nversion = \"0.2.0\"") {
		t.Fatalf("Cargo.lock not refreshed:\n%s", lock)
	}
}

func TestCargoWorkspacePackageTOMLEdit(t *testing.T) {
	in := []byte(`[workspace]
members = ["a"]

[workspace.package]
version = "1.0.0"

[workspace.dependencies]
version = "not this"
`)
	out, err := setTOML(in, []string{"package", "version"}, "1.1.0")
	if err != errNotFound {
		t.Fatalf("want errNotFound for [package], got %v %s", err, out)
	}
	out, err = setTOML(in, []string{"workspace", "package", "version"}, "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "[workspace.package]\nversion = \"1.1.0\"") || !strings.Contains(string(out), `version = "not this"`) {
		t.Fatalf("got\n%s", out)
	}
}

func TestRules(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "config/app.php", "<?php\nreturn [\n    'version' => '1.2.3',\n    'name' => 'app',\n];\n")
	write(t, dir, "chart/Chart.yaml", "apiVersion: v2\n# the app version\nappVersion: \"1.2.3\" # pinned\nversion: 0.1.0\n")
	write(t, dir, "meta.json", "{\"build\": {\"version\": \"1.2.3\"}}\n")
	write(t, dir, "pyproject.toml", "[project]\nname = \"x\"\nversion = \"1.2.3\"\n")
	rules := []config.FileRule{
		{File: "config/app.php", Regex: `'version' => '(?P<v>[^']+)'`},
		{File: "chart/Chart.yaml", Key: "appVersion"},
		{File: "meta.json", Key: "build.version"},
		{File: "pyproject.toml", Key: "project.version"},
	}
	if err := WriteRules(dir, rules, "1.3.0"); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		"config/app.php":   "<?php\nreturn [\n    'version' => '1.3.0',\n    'name' => 'app',\n];\n",
		"chart/Chart.yaml": "apiVersion: v2\n# the app version\nappVersion: \"1.3.0\" # pinned\nversion: 0.1.0\n",
		"meta.json":        "{\"build\": {\"version\": \"1.3.0\"}}\n",
		"pyproject.toml":   "[project]\nname = \"x\"\nversion = \"1.3.0\"\n",
	} {
		if got := read(t, dir, file); got != want {
			t.Errorf("%s =\n%s", file, got)
		}
	}

	if err := WriteRules(dir, []config.FileRule{{File: "config/app.php", Regex: `'build' => '([^']+)'`}}, "1"); err == nil {
		t.Fatal("want error when the regex does not match")
	}
	if err := WriteRules(dir, []config.FileRule{{File: "config/app.php", Key: "version"}}, "1"); err == nil {
		t.Fatal("want error for a key rule on a .php file")
	}
}

func TestYAMLPlainScalar(t *testing.T) {
	out, err := setYAML([]byte("a:\n  b: 1.0.0\n"), []string{"a", "b"}, "2.0.0")
	if err != nil || string(out) != "a:\n  b: 2.0.0\n" {
		t.Fatalf("got %q, %v", out, err)
	}
}
