package setup

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/nklmilojevic/vesnik/internal/ai"
	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/gitx"
)

// Context is what the model reads about the repository. Everything comes
// from tracked files and git metadata; ignored files such as .env are never
// read.
type Context struct {
	Name          string
	DefaultBranch string
	Branches      []string
	TagCount      int
	RecentTags    []string
	Subjects      []string
	Packages      []config.Package
	// Files maps a path to its (trimmed) content: README, manifests,
	// release recipes and workflow triggers.
	Files map[string]string
	// VersionLines are tracked lines that look like they hold a version.
	VersionLines []string
}

const (
	fileLimit = 3000
	// taskFileLimit is larger: a release recipe can sit deep in a long
	// justfile, and it decides the release branch.
	taskFileLimit = 8000
)

// Gather collects the context. Missing pieces are left empty.
func Gather(ctx context.Context, repo gitx.Repo, pkgs []config.Package) Context {
	c := Context{Name: filepath.Base(repo.Dir), Packages: pkgs, Files: map[string]string{}}
	c.DefaultBranch, _ = repo.DefaultBranch(ctx)
	if out, err := repo.Run(ctx, "branch", "-r", "--format=%(refname:short)"); err == nil {
		for b := range strings.SplitSeq(out, "\n") {
			if name, ok := strings.CutPrefix(b, "origin/"); ok && name != "HEAD" && name != "origin" {
				c.Branches = append(c.Branches, name)
			}
		}
	}
	if out, err := repo.Run(ctx, "tag", "--sort=-creatordate"); err == nil && out != "" {
		tags := strings.Split(out, "\n")
		c.TagCount = len(tags)
		c.RecentTags = tags[:min(15, len(tags))]
	}
	if out, err := repo.Run(ctx, "log", "--no-merges", "--format=%s", "-60"); err == nil && out != "" {
		c.Subjects = strings.Split(out, "\n")
	}

	tracked := map[string]bool{}
	if out, err := repo.Run(ctx, "ls-files"); err == nil {
		for f := range strings.SplitSeq(out, "\n") {
			tracked[f] = true
		}
	}
	read := func(path string, keep func(string) string) {
		limit := fileLimit
		if keep != nil && strings.Contains(path, "file") {
			limit = taskFileLimit
		}
		if !tracked[path] {
			return
		}
		data, err := os.ReadFile(filepath.Join(repo.Dir, path))
		if err != nil {
			return
		}
		text := string(data)
		if keep != nil {
			text = keep(text)
		}
		if text = strings.TrimSpace(text); text != "" {
			c.Files[path] = clip(text, limit)
		}
	}
	for _, f := range []string{"README.md", "README", "readme.md", "composer.json", "package.json", "Cargo.toml", "go.mod", "pyproject.toml"} {
		read(f, nil)
	}
	for _, f := range []string{"justfile", "Justfile", ".justfile", "Makefile"} {
		read(f, releaseLines)
	}
	var workflows []string
	for f := range tracked {
		if strings.HasPrefix(f, ".github/workflows/") && (strings.HasSuffix(f, ".yml") || strings.HasSuffix(f, ".yaml")) {
			workflows = append(workflows, f)
		}
	}
	slices.Sort(workflows)
	for _, f := range workflows {
		read(f, triggers)
	}

	if out, err := repo.Run(ctx, "grep", "-nIE", versionPattern, "--", ".",
		":(exclude)*.lock", ":(exclude)*-lock.json", ":(exclude)*lock.yaml", ":(exclude)vendor/**",
		":(exclude)node_modules/**", ":(exclude)*.env*", ":(exclude)*.min.js", ":(exclude)*.map", ":(exclude)CHANGELOG*"); err == nil {
		for line := range strings.SplitSeq(out, "\n") {
			if len(c.VersionLines) == 40 {
				break
			}
			// Minified files put a whole bundle on one line.
			c.VersionLines = append(c.VersionLines, clip(line, 200))
		}
	}
	return c
}

// versionPattern finds lines like 'version' => '1.2.3', VERSION = "1.2",
// or "version": "1.2.3".
const versionPattern = `(version|VERSION)['"]?[[:space:]]*(=>|=|:)[[:space:]]*['"]v?[0-9]+\.[0-9]+`

// releaseRe finds release recipes in task files. Deploy and version lines
// are too common in large task files to keep.
var releaseRe = regexp.MustCompile(`(?i)gh release|git tag|release|hotfix`)

// releaseLines keeps the lines of a task file that mention releases, with
// a little context.
func releaseLines(text string) string {
	lines := strings.Split(text, "\n")
	keep := map[int]bool{}
	for i, l := range lines {
		if releaseRe.MatchString(l) {
			for j := max(0, i-2); j <= min(len(lines)-1, i+4); j++ {
				keep[j] = true
			}
		}
	}
	var out []string
	for i, l := range lines {
		if keep[i] {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// triggers keeps the name and on: block of a workflow.
func triggers(text string) string {
	var out []string
	in := false
	for l := range strings.SplitSeq(text, "\n") {
		switch {
		case strings.HasPrefix(l, "name:"):
			out = append(out, l)
		case strings.HasPrefix(l, "on:"):
			in = true
			out = append(out, l)
		case in && (strings.HasPrefix(l, " ") || strings.TrimSpace(l) == ""):
			out = append(out, l)
		case in:
			in = false
		}
	}
	return strings.Join(out, "\n")
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[…]"
}

// Suggestion is the model's answer.
type Suggestion struct {
	Product   string     `json:"product"`
	Preset    string     `json:"preset"`
	Rules     []string   `json:"rules"`
	Branch    string     `json:"branch"`
	Changelog bool       `json:"changelog"`
	Files     []FileRule `json:"files"`
	Summary   string     `json:"summary"`
}

// FileRule is a suggested version file.
type FileRule struct {
	File  string `json:"file"`
	Regex string `json:"regex"`
}

const suggestSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["product", "preset", "rules", "branch", "changelog", "files", "summary"],
  "properties": {
    "product": {"type": "string"},
    "preset": {"type": "string", "enum": ["ste100", "keepachangelog", "casual"]},
    "rules": {"type": "array", "items": {"type": "string"}},
    "branch": {"type": "string"},
    "changelog": {"type": "boolean"},
    "files": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["file", "regex"],
        "properties": {"file": {"type": "string"}, "regex": {"type": "string"}}
      }
    },
    "summary": {"type": "string"}
  }
}`

// suggestSystem is the system prompt for the init model.
//
//go:embed prompt.md
var suggestSystem string

// Suggest asks the model and returns its answer.
func Suggest(ctx context.Context, p ai.Provider, c Context) (Suggestion, error) {
	raw, err := p.JSON(ctx, ai.Request{System: suggestSystem, User: c.Prompt(), Schema: suggestSchema})
	if err != nil {
		return Suggestion{}, err
	}
	var s Suggestion
	if err := json.Unmarshal(raw, &s); err != nil {
		return Suggestion{}, fmt.Errorf("model returned invalid JSON: %w", err)
	}
	return s, nil
}

// promptLimit caps the prompt in case a repository has unusual files the
// per-piece limits miss.
const promptLimit = 120_000

// Prompt renders the context for the model.
func (c Context) Prompt() string {
	return clip(c.prompt(), promptLimit)
}

func (c Context) prompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Repository: %s\nDefault branch: %s\nRemote branches: %s\n",
		c.Name, c.DefaultBranch, strings.Join(clipList(c.Branches, 40), ", "))
	fmt.Fprintf(&b, "Tags: %d in total, newest first: %s\n\n", c.TagCount, strings.Join(c.RecentTags, ", "))
	b.WriteString("Detected packages:\n")
	for _, p := range c.Packages {
		fmt.Fprintf(&b, "- %s at %s (%s)\n", p.Name, p.Path, p.Ecosystem)
	}
	b.WriteString("\nRecent commit subjects:\n")
	for _, s := range c.Subjects {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	paths := make([]string, 0, len(c.Files))
	for p := range c.Files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", p, c.Files[p])
	}
	b.WriteString("\n## Version candidates (path:line:text)\n\n")
	if len(c.VersionLines) == 0 {
		b.WriteString("none\n")
	}
	for _, l := range c.VersionLines {
		fmt.Fprintf(&b, "%s\n", l)
	}
	return b.String()
}

func clipList(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(slices.Clone(s[:n]), fmt.Sprintf("… %d more", len(s)-n))
}

// Apply validates a suggestion against the repository and merges what holds
// into the settings. It returns a note for every suggestion it dropped.
func Apply(root string, c Context, s Suggestion, set *Settings) []string {
	var dropped []string
	set.Product = strings.TrimSpace(s.Product)
	if slices.Contains(config.Presets, s.Preset) {
		set.Preset = s.Preset
	}
	for _, r := range s.Rules {
		if r = strings.TrimSpace(r); r != "" && len(set.Rules) < 6 {
			set.Rules = append(set.Rules, r)
		}
	}
	switch branch := strings.TrimSpace(s.Branch); {
	case branch == "" || branch == c.DefaultBranch:
	case slices.Contains(c.Branches, branch):
		set.Branch = branch
	default:
		dropped = append(dropped, fmt.Sprintf("branch %q: no such branch on origin", branch))
	}
	set.Changelog = s.Changelog
	set.Summary = strings.TrimSpace(s.Summary)

	if len(set.Packages) != 1 || set.Packages[0].Path != "." {
		if len(s.Files) > 0 {
			dropped = append(dropped, "version files: only applied to single-package repositories; add them per package by hand")
		}
		return dropped
	}
	for _, f := range s.Files {
		if err := checkRule(root, f); err != nil {
			dropped = append(dropped, fmt.Sprintf("version file %s: %v", f.File, err))
			continue
		}
		set.Packages[0].Files = append(set.Packages[0].Files, config.FileRule{File: f.File, Regex: f.Regex})
	}
	return dropped
}

func checkRule(root string, f FileRule) error {
	if f.File == "" || filepath.IsAbs(f.File) || strings.HasPrefix(filepath.Clean(f.File), "..") {
		return fmt.Errorf("path must be inside the repository")
	}
	re, err := regexp.Compile(f.Regex)
	if err != nil {
		return fmt.Errorf("bad regex: %w", err)
	}
	if re.SubexpIndex("v") < 0 && re.NumSubexp() < 1 {
		return fmt.Errorf("regex has no capture group")
	}
	data, err := os.ReadFile(filepath.Join(root, f.File))
	if err != nil {
		return fmt.Errorf("cannot read file")
	}
	if !re.Match(data) {
		return fmt.Errorf("regex does not match the file")
	}
	return nil
}
