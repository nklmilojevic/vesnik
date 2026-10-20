package version

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Placeholder is the token a tag template replaces with the version.
const Placeholder = "{version}"

// Template renders and parses tags such as "v{version}" or "api@v{version}".
type Template struct {
	prefix, suffix string
}

// ParseTemplate validates a tag template.
func ParseTemplate(s string) (Template, error) {
	prefix, suffix, ok := strings.Cut(s, Placeholder)
	if !ok || strings.Contains(suffix, Placeholder) {
		return Template{}, fmt.Errorf("tag template %q must contain %s exactly once", s, Placeholder)
	}
	return Template{prefix: prefix, suffix: suffix}, nil
}

// Render returns the tag for v.
func (t Template) Render(v *semver.Version) string {
	return t.prefix + v.String() + t.suffix
}

// Glob returns a git tag pattern matching every tag of this template.
func (t Template) Glob() string {
	return t.prefix + "*" + t.suffix
}

// Parse returns the version in tag, or false when tag does not belong to
// this template.
func (t Template) Parse(tag string) (*semver.Version, bool) {
	rest, ok := strings.CutPrefix(tag, t.prefix)
	if !ok {
		return nil, false
	}
	rest, ok = strings.CutSuffix(rest, t.suffix)
	if !ok {
		return nil, false
	}
	v, err := semver.StrictNewVersion(rest)
	if err != nil {
		return nil, false
	}
	return v, true
}

// Tagged is a tag with its parsed version.
type Tagged struct {
	Tag     string
	Version *semver.Version
}

// Latest returns the highest tag overall and the highest final (non
// pre-release) tag among tags that match t. Either is zero when absent.
func (t Template) Latest(tags []string) (latest, final Tagged) {
	var all []Tagged
	for _, tag := range tags {
		if v, ok := t.Parse(tag); ok {
			all = append(all, Tagged{Tag: tag, Version: v})
		}
	}
	slices.SortFunc(all, func(a, b Tagged) int { return a.Version.Compare(b.Version) })
	for _, t := range slices.Backward(all) {
		if latest.Tag == "" {
			latest = t
		}
		if t.Version.Prerelease() == "" {
			final = t
			break
		}
	}
	return latest, final
}
