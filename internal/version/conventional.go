package version

import (
	"regexp"
	"strings"
)

var header = regexp.MustCompile(`^(\w+)(\([^)]*\))?(!)?:`)

// Floor returns the smallest bump the conventional commit messages call for:
// major for a "!" or a BREAKING CHANGE footer, minor for feat, patch otherwise.
// The AI may suggest more, never less without a warning.
func Floor(messages []string) Bump {
	floor := Patch
	for _, msg := range messages {
		subject, body, _ := strings.Cut(msg, "\n")
		m := header.FindStringSubmatch(subject)
		if m != nil && m[3] == "!" || breaking(body) {
			return Major
		}
		if m != nil && strings.EqualFold(m[1], "feat") {
			floor = Minor
		}
	}
	return floor
}

func breaking(body string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "BREAKING CHANGE:") || strings.HasPrefix(line, "BREAKING-CHANGE:") {
			return true
		}
	}
	return false
}
