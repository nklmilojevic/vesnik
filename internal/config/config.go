// Package config loads .vesnik.yaml. Every field is optional: a repository
// without the file is one package at the root, tagged v{version}, with the
// ecosystem detected from its files.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nklmilojevic/vesnik/internal/version"
)

// FileName is the config file at the repository root.
const FileName = ".vesnik.yaml"

// Config is the whole .vesnik.yaml file.
type Config struct {
	Provider  string    `yaml:"provider"`
	Model     string    `yaml:"model"`
	Vertex    Vertex    `yaml:"vertex"`
	AI        AI        `yaml:"ai"`
	Style     Style     `yaml:"style"`
	GitHub    GitHub    `yaml:"github"`
	Changelog Changelog `yaml:"changelog"`
	Branch    string    `yaml:"branch"`
	Remote    string    `yaml:"remote"`
	// BreakingBumpsMinor keeps breaking changes on a minor bump below 1.0.0.
	BreakingBumpsMinor *bool `yaml:"breaking_bumps_minor"`
	// MaxBump caps the bump the model or the fallback picks; explicit
	// --bump and changes in the review are not capped.
	MaxBump  string    `yaml:"max_bump"`
	Packages []Package `yaml:"packages"`
}

// AI configures how a model failure is handled.
type AI struct {
	// OnError is "fail" to stop the release, or "plain" to release with
	// GitHub's generated notes and the requested or commit-type bump.
	OnError string `yaml:"on_error"`
}

// Vertex configures the vertex provider.
type Vertex struct {
	Project string `yaml:"project"`
	Region  string `yaml:"region"`
}

// Style shapes the release notes.
type Style struct {
	Preset     string   `yaml:"preset"`
	Product    string   `yaml:"product"`
	Sections   []string `yaml:"sections"`
	Rules      []string `yaml:"rules"`
	PromptFile string   `yaml:"prompt_file"`
}

// GitHub configures the GitHub Release.
type GitHub struct {
	AppendGeneratedNotes *bool `yaml:"append_generated_notes"`
}

// Changelog configures the changelog file written in each package.
type Changelog struct {
	// Enabled turns the changelog off for repositories that only tag; then
	// a release without version files makes no commit.
	Enabled *bool  `yaml:"enabled"`
	File    string `yaml:"file"`
}

// PathFor returns a package's changelog path relative to the repository
// root, or "" when changelogs are off.
func (c Changelog) PathFor(pkgPath string) string {
	if !*c.Enabled {
		return ""
	}
	return filepath.ToSlash(filepath.Join(pkgPath, c.File))
}

// Package is one independently versioned unit.
type Package struct {
	Name      string     `yaml:"name"`
	Path      string     `yaml:"path"`
	Ecosystem string     `yaml:"ecosystem"`
	Tag       string     `yaml:"tag"`
	Files     []FileRule `yaml:"files"`
	// DefaultTag is true when Tag was not configured, so the release may
	// adopt the tag format the repository already uses.
	DefaultTag bool `yaml:"-"`
}

// FileRule is an extra file that carries the version. Exactly one of Regex
// (with a group named v, or the first group) or Key (a dotted key path in a
// JSON, YAML or TOML file) is set.
type FileRule struct {
	File  string `yaml:"file"`
	Regex string `yaml:"regex"`
	Key   string `yaml:"key"`
}

// DefaultModel is the model for the Claude providers when none is set.
const DefaultModel = "claude-sonnet-5-5"

// Providers lists the supported provider names.
var Providers = []string{"claude", "codex", "anthropic", "vertex"}

// Ecosystems lists the supported ecosystem names; "auto" detects one.
var Ecosystems = []string{"auto", "cargo", "go", "node", "php", "none"}

// Presets lists the built-in style presets.
var Presets = []string{"ste100", "keepachangelog", "casual"}

// Load reads root/.vesnik.yaml, or returns defaults when it does not exist.
func Load(root string) (*Config, error) {
	cfg := &Config{}
	data, err := os.ReadFile(filepath.Join(root, FileName))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: %w", FileName, err)
		}
	}
	cfg.defaults(filepath.Base(root))
	if err := cfg.validate(root); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return cfg, nil
}

func (c *Config) defaults(repoName string) {
	if c.AI.OnError == "" {
		c.AI.OnError = "fail"
	}
	if c.Provider == "" {
		c.Provider = "claude"
	}
	if c.Model == "" && c.Provider != "codex" {
		c.Model = DefaultModel
	}
	if c.Vertex.Region == "" {
		c.Vertex.Region = "global"
	}
	if c.Style.Preset == "" {
		c.Style.Preset = "ste100"
	}
	if c.GitHub.AppendGeneratedNotes == nil {
		c.GitHub.AppendGeneratedNotes = new(true)
	}
	if c.Changelog.Enabled == nil {
		c.Changelog.Enabled = new(true)
	}
	if c.Changelog.File == "" {
		c.Changelog.File = "CHANGELOG.md"
	}
	if c.Remote == "" {
		c.Remote = "origin"
	}
	if c.BreakingBumpsMinor == nil {
		c.BreakingBumpsMinor = new(true)
	}
	if len(c.Packages) == 0 {
		c.Packages = []Package{{Name: repoName, Path: "."}}
	}
	multi := len(c.Packages) > 1
	for i := range c.Packages {
		p := &c.Packages[i]
		if p.Path == "" {
			p.Path = "."
		}
		p.Path = filepath.ToSlash(filepath.Clean(p.Path))
		if p.Name == "" {
			if p.Path == "." {
				p.Name = repoName
			} else {
				p.Name = filepath.Base(p.Path)
			}
		}
		if p.Ecosystem == "" {
			p.Ecosystem = "auto"
		}
		if p.Tag == "" {
			p.DefaultTag = true
			if multi {
				p.Tag = p.Name + "@v" + version.Placeholder
			} else {
				p.Tag = "v" + version.Placeholder
			}
		}
	}
}

func (c *Config) validate(root string) error {
	if c.MaxBump != "" {
		if _, err := version.ParseBump(c.MaxBump); err != nil {
			return fmt.Errorf("max_bump: %w", err)
		}
	}
	if c.AI.OnError != "fail" && c.AI.OnError != "plain" {
		return fmt.Errorf("ai.on_error %q: want fail or plain", c.AI.OnError)
	}
	if !slices.Contains(Providers, c.Provider) {
		return fmt.Errorf("provider %q: want one of %s", c.Provider, strings.Join(Providers, ", "))
	}
	if c.Style.PromptFile == "" && !slices.Contains(Presets, c.Style.Preset) {
		return fmt.Errorf("style.preset %q: want one of %s", c.Style.Preset, strings.Join(Presets, ", "))
	}
	names := map[string]bool{}
	tags := map[string]bool{}
	for _, p := range c.Packages {
		if names[p.Name] {
			return fmt.Errorf("package name %q is used twice", p.Name)
		}
		names[p.Name] = true
		if strings.HasPrefix(p.Path, "..") || filepath.IsAbs(p.Path) {
			return fmt.Errorf("package %s: path %q must stay inside the repository", p.Name, p.Path)
		}
		if info, err := os.Stat(filepath.Join(root, p.Path)); err != nil || !info.IsDir() {
			return fmt.Errorf("package %s: path %q is not a directory", p.Name, p.Path)
		}
		if !slices.Contains(Ecosystems, p.Ecosystem) {
			return fmt.Errorf("package %s: ecosystem %q: want one of %s", p.Name, p.Ecosystem, strings.Join(Ecosystems, ", "))
		}
		tmpl, err := version.ParseTemplate(p.Tag)
		if err != nil {
			return fmt.Errorf("package %s: %w", p.Name, err)
		}
		if tags[tmpl.Glob()] {
			return fmt.Errorf("package %s: tag %q overlaps another package's tags", p.Name, p.Tag)
		}
		tags[tmpl.Glob()] = true
		for _, f := range p.Files {
			if f.File == "" || (f.Regex == "") == (f.Key == "") {
				return fmt.Errorf("package %s: each files entry needs file and exactly one of regex or key", p.Name)
			}
		}
	}
	return nil
}

// Find returns the packages named in names, or all of them for no names.
func (c *Config) Find(names []string) ([]Package, error) {
	if len(names) == 0 {
		return c.Packages, nil
	}
	var out []Package
	for _, n := range names {
		i := slices.IndexFunc(c.Packages, func(p Package) bool { return p.Name == n })
		if i < 0 {
			return nil, fmt.Errorf("unknown package %q", n)
		}
		out = append(out, c.Packages[i])
	}
	return out, nil
}
