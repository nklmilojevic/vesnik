// Package setup writes a starter .vesnik.yaml. It detects packages and the
// tag format from the repository, and can ask a model to read the repo and
// suggest the parts that need judgement: what the product is, the style
// rules, the release branch, whether to keep a changelog, and where a
// version string lives in code. Every suggestion is checked against the
// repository before it is written.
package setup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/ecosystem"
)

// monorepoDirs are the conventional parents of independently released
// packages.
var monorepoDirs = []string{"packages", "apps", "services", "crates", "libs", "modules"}

// Detect finds packages under the monorepo directories; without at least
// two, the repository root is the one package.
func Detect(root string) ([]config.Package, error) {
	var pkgs []config.Package
	for _, parent := range monorepoDirs {
		entries, err := os.ReadDir(filepath.Join(root, parent))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := filepath.Join(parent, e.Name())
			b, _ := ecosystem.For("auto", filepath.Join(root, dir))
			if b.Name() == "none" {
				continue
			}
			pkgs = append(pkgs, config.Package{Name: e.Name(), Path: filepath.ToSlash(dir), Ecosystem: b.Name()})
		}
	}
	if len(pkgs) < 2 {
		b, _ := ecosystem.For("auto", root)
		return []config.Package{{Name: filepath.Base(root), Path: ".", Ecosystem: b.Name()}}, nil
	}
	slices.SortFunc(pkgs, func(a, b config.Package) int { return strings.Compare(a.Path, b.Path) })
	return pkgs, nil
}

// Settings are the values the starter file is rendered from.
type Settings struct {
	Provider  string
	Model     string
	Preset    string
	Product   string
	Rules     []string
	Branch    string
	Changelog bool
	Packages  []config.Package
	// Summary explains AI suggestions; it becomes a comment at the top.
	Summary string
}

// Defaults returns the settings init writes without a model.
func Defaults(pkgs []config.Package) Settings {
	return Settings{Provider: "claude", Model: config.DefaultModel, Preset: "ste100", Changelog: true, Packages: pkgs}
}

// q quotes a string for YAML; JSON strings are valid YAML scalars.
func q(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Render writes the starter file.
func Render(s Settings) string {
	var b strings.Builder
	b.WriteString("# vesnik release configuration. Every field is optional.\n")
	if s.Summary != "" {
		b.WriteString("#\n")
		for line := range strings.SplitSeq(wrap(s.Summary, 76), "\n") {
			b.WriteString("# " + line + "\n")
		}
	}
	fmt.Fprintf(&b, `
# Model provider: claude (the claude CLI), codex (the codex CLI),
# anthropic (ANTHROPIC_API_KEY or an ant login) or vertex (Google ADC).
provider: %s
`, s.Provider)
	if s.Model != "" {
		fmt.Fprintf(&b, "model: %s\n", s.Model)
	}
	fmt.Fprintf(&b, `
style:
  # ste100, keepachangelog or casual
  preset: %s
  # One line about what the project is, for the model.
  product: %s
  # Extra instructions for the model.
`, s.Preset, q(s.Product))
	if len(s.Rules) == 0 {
		b.WriteString("  rules: []\n")
	} else {
		b.WriteString("  rules:\n")
		for _, r := range s.Rules {
			fmt.Fprintf(&b, "    - %s\n", q(r))
		}
	}
	b.WriteString(`
github:
  # Append GitHub's generated pull request list under the notes.
  append_generated_notes: true
`)
	if s.Changelog {
		b.WriteString(`
changelog:
  file: CHANGELOG.md
`)
	} else {
		b.WriteString(`
changelog:
  # Off: a release without version files tags the branch head and makes no
  # commit.
  enabled: false
`)
	}
	b.WriteString(`
# Keep breaking changes on a minor bump while the version is below 1.0.0.
breaking_bumps_minor: true
`)
	if s.Branch != "" {
		fmt.Fprintf(&b, "\n# Releases are cut from this branch, not origin's default branch.\nbranch: %s\n", s.Branch)
	}
	b.WriteString("\n")
	b.WriteString(renderPackages(s.Packages))
	return b.String()
}

func renderPackages(pkgs []config.Package) string {
	var b strings.Builder
	if len(pkgs) == 1 && pkgs[0].Path == "." {
		p := pkgs[0]
		if len(p.Files) == 0 {
			fmt.Fprintf(&b, `# One package at the repository root, tagged v{version} (or {version} when
# the repository already uses bare version tags). To version extra files:
#
# packages:
#   - path: .
#     ecosystem: %s
#     files:
#       - file: config/app.php
#         regex: "'version' => '(?P<v>[^']+)'"
`, p.Ecosystem)
			return b.String()
		}
		fmt.Fprintf(&b, "packages:\n  - path: .\n    ecosystem: %s\n    files:\n", p.Ecosystem)
		for _, f := range p.Files {
			fmt.Fprintf(&b, "      - file: %s\n        regex: %s\n", q(f.File), q(f.Regex))
		}
		return b.String()
	}
	b.WriteString("# Each package is released on its own, tagged <name>@v{version}.\npackages:\n")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "  - name: %s\n    path: %s\n    ecosystem: %s\n", p.Name, p.Path, p.Ecosystem)
	}
	return b.String()
}

func wrap(s string, width int) string {
	var lines []string
	var line string
	for word := range strings.FieldsSeq(s) {
		if line != "" && len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
