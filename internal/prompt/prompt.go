// Package prompt builds the system and user prompts for release notes and
// the bump suggestion, from a style preset and the collected changes.
package prompt

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Masterminds/semver/v3"

	"github.com/nklmilojevic/vesnik/internal/collect"
	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/version"
)

//go:embed presets/*.md
var presets embed.FS

var presetSections = map[string][]string{
	"ste100":         {"New features", "Fixes", "Other changes"},
	"keepachangelog": {"Added", "Changed", "Deprecated", "Removed", "Fixed", "Security"},
	"casual":         {"New", "Fixed", "Under the hood"},
}

// Input is everything the prompts describe.
type Input struct {
	// Project is the product name the notes are about.
	Project string
	Package config.Package
	// Multi is true when the repository releases more than one package.
	Multi   bool
	Current *semver.Version
	LastTag string
	Floor   version.Bump
	Changes collect.Set
}

// System returns the system prompt for a style. root resolves prompt_file.
func System(root string, style config.Style, in Input) (string, error) {
	sections := style.Sections
	if len(sections) == 0 {
		sections = presetSections[style.Preset]
	}
	data := map[string]any{
		"Project":  in.Project,
		"Product":  style.Product,
		"Sections": sections,
		"Rules":    style.Rules,
		"Pre1":     in.Current == nil || in.Current.Major() == 0,
	}
	if style.PromptFile != "" {
		raw, err := os.ReadFile(filepath.Join(root, style.PromptFile))
		if err != nil {
			return "", fmt.Errorf("style.prompt_file: %w", err)
		}
		return render(string(raw), data)
	}
	body, err := presets.ReadFile("presets/" + style.Preset + ".md")
	if err != nil {
		return "", fmt.Errorf("unknown style preset %q", style.Preset)
	}
	data["Style"] = strings.TrimSpace(string(body))
	return render(systemTemplate, data)
}

const systemTemplate = `You write release notes for {{.Project}}{{with .Product}}, {{.}}{{end}}.
{{.Style}}
{{- range .Rules}}
- {{.}}
{{- end}}

Write the notes field as Markdown only, with no title, no version heading, no preamble and no closing remark.
Be brief. Start with one or two sentences that tell what the release gives to the user.
Then use these level-two headings in this order: {{range $i, $s := .Sections}}{{if $i}}, {{end}}'## {{$s}}'{{end}}. Leave out a heading that has no items.
Give each pull request, or each commit without a pull request, one bullet with one or two short sentences: what changed and why it is important to the user.
Do not describe edge cases, safeguards, or implementation details. Combine follow-up fixes into the change they fix. Leave out dependency updates, CI, and release chores, unless they change behavior for the user.
Put inline code in backticks. Do not include links, pull request numbers, commit hashes, or author names.

Also suggest the semantic version bump, judged from the user's side:
- major: a user must change something to keep working (removed or renamed commands, flags, config, API, or changed defaults that break existing setups).
- minor: new features or new behavior that users can opt into.
- patch: fixes, documentation, performance, and internal changes only.
{{- if .Pre1}}
The project is below 1.0.0. Still answer major for breaking changes; the tool maps that to a minor bump when the project wants it.
{{- end}}
The commit types are a hint, not the answer: a "fix:" that changes a default can be breaking, and a "feat:" that only adds an internal helper is a patch.
Put one or two sentences in the reasoning field that name the change that decided the bump.`

// User returns the user prompt that lists the changes.
func User(in Input) string {
	var b strings.Builder
	name := in.Project
	if in.Multi {
		name = fmt.Sprintf("%s, package %s (path %s)", in.Project, in.Package.Name, in.Package.Path)
	}
	fmt.Fprintf(&b, "Write release notes for %s.\n", name)
	if in.Current != nil {
		fmt.Fprintf(&b, "The current version is %s (tag %s).\n", in.Current, in.LastTag)
	} else {
		b.WriteString("This is the first release.\n")
	}
	fmt.Fprintf(&b, "The conventional commit types alone call for at least a %s bump.\n\n", in.Floor)

	b.WriteString("## Commits\n\n")
	for _, c := range in.Changes.Changes {
		fmt.Fprintf(&b, "### %s\n", c.Commit.Subject)
		if c.Commit.Body != "" {
			fmt.Fprintf(&b, "\n%s\n", c.Commit.Body)
		}
		for _, pr := range c.PRs {
			fmt.Fprintf(&b, "\nPull request: %s\n", pr.Title)
		}
		b.WriteString("\n")
	}

	if prs := in.Changes.PRs(); len(prs) > 0 {
		b.WriteString("## Pull requests\n\n")
		for _, pr := range prs {
			fmt.Fprintf(&b, "### %s\n", pr.Title)
			if len(pr.Labels) > 0 {
				fmt.Fprintf(&b, "Labels: %s\n", strings.Join(pr.Labels, ", "))
			}
			if body := clip(strings.TrimSpace(pr.Body), 4000); body != "" {
				fmt.Fprintf(&b, "\n%s\n", body)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// clip shortens very long pull request bodies (checklists, logs, screenshots)
// that add tokens without adding meaning.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[…]"
}

func render(tmpl string, data any) (string, error) {
	t, err := template.New("prompt").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}
