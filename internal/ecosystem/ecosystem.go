// Package ecosystem writes a new version into the files a package carries it
// in: Cargo.toml, package.json, composer.json, or user-defined files. The git
// tag is always the source of truth for the current version; these files
// only follow it.
package ecosystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nklmilojevic/vesnik/internal/config"
)

// versionKey is the manifest key that holds the version.
const versionKey = "version"

// Bumper writes a version for one ecosystem.
type Bumper interface {
	// Name is the ecosystem name used in .vesnik.yaml.
	Name() string
	// Detect reports whether dir holds a package of this ecosystem.
	Detect(dir string) bool
	// Files describes the files Write changes in dir, for the release plan.
	Files(dir string) []string
	// Write sets the version in dir.
	Write(ctx context.Context, dir, version string) error
}

// bumpers is in detection order. PHP and Go win over Node: a Laravel app or
// a Go tool with a package.json for front-end tooling is not a Node package.
var bumpers = []Bumper{Cargo{}, Go{}, PHP{}, Node{}}

// For returns the bumper for an ecosystem name; "auto" detects it from dir
// and falls back to tag-only.
func For(name, dir string) (Bumper, error) {
	if name == "auto" {
		for _, b := range bumpers {
			if b.Detect(dir) {
				return b, nil
			}
		}
		return None{}, nil
	}
	if name == "none" {
		return None{}, nil
	}
	for _, b := range bumpers {
		if b.Name() == name {
			return b, nil
		}
	}
	return nil, fmt.Errorf("unknown ecosystem %q", name)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func rewrite(path string, edit func([]byte) ([]byte, error)) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := edit(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, info.Mode())
}

// Cargo bumps [package] or [workspace.package] in Cargo.toml and refreshes
// Cargo.lock offline.
type Cargo struct{}

func (Cargo) Name() string           { return "cargo" }
func (Cargo) Detect(dir string) bool { return exists(filepath.Join(dir, "Cargo.toml")) }
func (Cargo) Files(string) []string  { return []string{"Cargo.toml", "Cargo.lock"} }

func (Cargo) Write(ctx context.Context, dir, version string) error {
	err := rewrite(filepath.Join(dir, "Cargo.toml"), func(b []byte) ([]byte, error) {
		out, err := setTOML(b, []string{"package", versionKey}, version)
		if errors.Is(err, errNotFound) {
			out, err = setTOML(b, []string{"workspace", "package", versionKey}, version)
		}
		if errors.Is(err, errNotFound) {
			return nil, errors.New("no version in [package] or [workspace.package]; " +
				"a version inherited from the workspace is bumped by releasing the workspace root")
		}
		return out, err
	})
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("cargo"); err != nil {
		return errors.New("cargo is not on PATH; it is needed to refresh Cargo.lock")
	}
	cmd := exec.CommandContext(ctx, "cargo", "update", "--workspace", "--offline")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cargo update --workspace --offline: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Node bumps package.json and the root entries of package-lock.json when
// package.json has a version. Private applications often do not, and are
// released by tag alone.
type Node struct{}

func (Node) Name() string           { return "node" }
func (Node) Detect(dir string) bool { return exists(filepath.Join(dir, "package.json")) }

func (Node) Files(dir string) []string {
	if !hasVersion(filepath.Join(dir, "package.json")) {
		return nil
	}
	files := []string{"package.json"}
	if exists(filepath.Join(dir, "package-lock.json")) {
		files = append(files, "package-lock.json")
	}
	return files
}

func (Node) Write(_ context.Context, dir, version string) error {
	if !hasVersion(filepath.Join(dir, "package.json")) {
		return nil
	}
	if err := rewrite(filepath.Join(dir, "package.json"), func(b []byte) ([]byte, error) {
		return setJSON(b, []string{versionKey}, version)
	}); err != nil {
		return err
	}
	lock := filepath.Join(dir, "package-lock.json")
	if !exists(lock) {
		return nil
	}
	return rewrite(lock, func(b []byte) ([]byte, error) {
		out, err := setJSON(b, []string{versionKey}, version)
		if err != nil {
			return nil, err
		}
		out, err = setJSON(out, []string{"packages", "", versionKey}, version)
		if errors.Is(err, errNotFound) {
			return out, nil
		}
		return out, err
	})
}

// PHP bumps the version field of composer.json when the package declares
// one. Most PHP applications do not, and are released by tag alone.
type PHP struct{}

func (PHP) Name() string           { return "php" }
func (PHP) Detect(dir string) bool { return exists(filepath.Join(dir, "composer.json")) }

func (PHP) Files(dir string) []string {
	if hasVersion(filepath.Join(dir, "composer.json")) {
		return []string{"composer.json"}
	}
	return nil
}

// hasVersion reports whether a JSON manifest has a top-level version.
func hasVersion(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, err = getJSON(data, []string{versionKey})
	return err == nil
}

func (PHP) Write(_ context.Context, dir, version string) error {
	if !hasVersion(filepath.Join(dir, "composer.json")) {
		return nil
	}
	return rewrite(filepath.Join(dir, "composer.json"), func(b []byte) ([]byte, error) {
		return setJSON(b, []string{versionKey}, version)
	})
}

// Go modules carry their version in the tag only.
type Go struct{}

func (Go) Name() string                                { return "go" }
func (Go) Detect(dir string) bool                      { return exists(filepath.Join(dir, "go.mod")) }
func (Go) Files(string) []string                       { return nil }
func (Go) Write(context.Context, string, string) error { return nil }

// None is a tag-only package.
type None struct{}

func (None) Name() string                                { return "none" }
func (None) Detect(string) bool                          { return true }
func (None) Files(string) []string                       { return nil }
func (None) Write(context.Context, string, string) error { return nil }

// WriteRules applies the user's extra file rules, relative to dir.
func WriteRules(dir string, rules []config.FileRule, version string) error {
	for _, r := range rules {
		path := filepath.Join(dir, r.File)
		err := rewrite(path, func(b []byte) ([]byte, error) {
			if r.Regex != "" {
				return setRegex(b, r.Regex, version)
			}
			keys := strings.Split(r.Key, ".")
			switch strings.ToLower(filepath.Ext(r.File)) {
			case ".json":
				return setJSON(b, keys, version)
			case ".yaml", ".yml":
				return setYAML(b, keys, version)
			case ".toml":
				return setTOML(b, keys, version)
			default:
				return nil, fmt.Errorf("key rules support .json, .yaml and .toml; use regex for %s", r.File)
			}
		})
		if errors.Is(err, errNotFound) {
			return fmt.Errorf("%s: no match for %s%s", r.File, r.Regex, r.Key)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
