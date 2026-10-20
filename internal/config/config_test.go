package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestLoadDefaultsWithoutFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sofka")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "claude" || cfg.Model != "claude-sonnet-5-5" || cfg.Style.Preset != "ste100" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if !*cfg.GitHub.AppendGeneratedNotes || !*cfg.BreakingBumpsMinor || cfg.Changelog.File != "CHANGELOG.md" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if len(cfg.Packages) != 1 {
		t.Fatalf("packages = %+v", cfg.Packages)
	}
	p := cfg.Packages[0]
	if p.Name != "sofka" || p.Path != "." || p.Tag != "v{version}" || p.Ecosystem != "auto" {
		t.Fatalf("package = %+v", p)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, FileName, "")
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMonorepoDefaultsTagsPerPackage(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "packages/api/composer.json", "{}")
	write(t, dir, "packages/web/package.json", "{}")
	write(t, dir, FileName, `
provider: codex
packages:
  - path: packages/api
    ecosystem: php
    files:
      - file: config/app.php
        regex: "'version' => '(?P<v>[^']+)'"
  - name: frontend
    path: ./packages/web/
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "" {
		t.Fatalf("codex keeps its own default model, got %q", cfg.Model)
	}
	api, web := cfg.Packages[0], cfg.Packages[1]
	if api.Name != "api" || api.Tag != "api@v{version}" || api.Ecosystem != "php" {
		t.Fatalf("api = %+v", api)
	}
	if web.Name != "frontend" || web.Path != "packages/web" || web.Tag != "frontend@v{version}" {
		t.Fatalf("web = %+v", web)
	}
	got, err := cfg.Find([]string{"frontend"})
	if err != nil || len(got) != 1 || got[0].Name != "frontend" {
		t.Fatalf("Find = %+v, %v", got, err)
	}
	if _, err := cfg.Find([]string{"nope"}); err == nil {
		t.Fatal("want error for unknown package")
	}
}

func TestLoadRejects(t *testing.T) {
	tests := map[string]string{
		"unknown field":     "provder: claude",
		"bad provider":      "provider: openai",
		"bad preset":        "style: {preset: pirate}",
		"missing path":      "packages: [{path: nope}]",
		"escaping path":     "packages: [{path: ../x}]",
		"bad ecosystem":     "packages: [{ecosystem: maven}]",
		"tag without token": "packages: [{tag: release}]",
		"duplicate names":   "packages: [{name: a}, {name: a}]",
		"same tags":         "packages: [{name: a, tag: 'v{version}'}, {name: b, tag: 'v{version}'}]",
		"file rule both":    "packages: [{files: [{file: x, regex: 'a', key: b}]}]",
		"file rule neither": "packages: [{files: [{file: x}]}]",
		"bad ai on_error":   "ai: {on_error: ignore}",
		"bad max_bump":      "max_bump: huge",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, FileName, content)
			_, err := Load(dir)
			if err == nil {
				t.Fatal("want error")
			}
			if !strings.Contains(err.Error(), FileName) {
				t.Fatalf("error does not name the file: %v", err)
			}
		})
	}
}
