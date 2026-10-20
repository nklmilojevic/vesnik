package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/gitx/gittest"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := Execute(context.Background(), "v1.2.3", args, &out, &errOut)
	return out.String() + errOut.String(), err
}

func TestInitMonorepo(t *testing.T) {
	tr := gittest.New(t)
	tr.Write("packages/api/composer.json", "{}")
	tr.Write("packages/web/package.json", "{}")
	tr.Write("packages/docs/README.md", "not a package")
	tr.Write("services/worker/go.mod", "module w")

	out, err := run(t, "init", "--no-ai", "-C", tr.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "3 package(s)") {
		t.Fatalf("out = %s", out)
	}
	cfg, err := config.Load(tr.Dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(cfg.Packages))
	for _, p := range cfg.Packages {
		got = append(got, p.Name+":"+p.Ecosystem+":"+p.Tag)
	}
	want := "api:php:api@v{version} web:node:web@v{version} worker:go:worker@v{version}"
	if strings.Join(got, " ") != want {
		t.Fatalf("packages = %v", got)
	}

	if _, err := run(t, "init", "--no-ai", "-C", tr.Dir); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second init: %v", err)
	}
	if _, err := run(t, "init", "--no-ai", "-C", tr.Dir, "--force"); err != nil {
		t.Fatal(err)
	}
}

func TestInitSinglePackage(t *testing.T) {
	tr := gittest.New(t)
	tr.Write("composer.json", "{}")
	if _, err := run(t, "init", "--no-ai", "-C", tr.Dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(tr.Dir, config.FileName))
	if !strings.Contains(string(data), "#     ecosystem: php") {
		t.Fatalf("starter:\n%s", data)
	}
	cfg, err := config.Load(tr.Dir)
	if err != nil || len(cfg.Packages) != 1 || cfg.Packages[0].Tag != "v{version}" {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
}

func TestReleaseNeedsTerminalOrYes(t *testing.T) {
	// go test runs with stdin that is not a terminal.
	_, err := run(t, "release", "-C", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("err = %v", err)
	}
}

func TestVersionAndBadBump(t *testing.T) {
	out, err := run(t, "--version")
	if err != nil || !strings.Contains(out, "v1.2.3") {
		t.Fatalf("version: %q, %v", out, err)
	}
	if _, err := run(t, "plan", "--bump", "huge"); err == nil || !strings.Contains(err.Error(), "unknown bump") {
		t.Fatalf("bad bump: %v", err)
	}
}

func TestInitAsksTheModel(t *testing.T) {
	tr := gittest.New(t)
	tr.CommitFile("composer.json", `{"name": "acme/app"}`, "ABC-1: init")
	tr.Git("tag", "v1.0.0")

	bin := t.TempDir()
	script := `#!/bin/sh
cat > /dev/null
printf '%s\n' '{"is_error":false,"result":"","structured_output":{"product":"Acme app for engineers","preset":"ste100","rules":["Keep ticket keys."],"branch":"","changelog":false,"files":[],"summary":"Tag-only releases."}}'
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := run(t, "init", "-C", tr.Dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Tag-only releases.") {
		t.Fatalf("out = %s", out)
	}
	cfg, err := config.Load(tr.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Style.Product != "Acme app for engineers" || *cfg.Changelog.Enabled || cfg.Style.Rules[0] != "Keep ticket keys." {
		t.Fatalf("cfg = %+v", cfg)
	}
}
