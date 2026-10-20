// Package changelog prepends a release section to a CHANGELOG.md file.
package changelog

import (
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

const title = "# Changelog\n"

// Section renders one release: a level-two heading with the version and
// date, then the notes with their headings demoted one level so they nest
// under it.
func Section(heading, date, notes string) string {
	return "## " + heading + " - " + date + "\n\n" + Demote(strings.TrimSpace(notes)) + "\n"
}

var atx = regexp.MustCompile(`^(#{1,5})(\s)`)

// Demote adds one # to every ATX heading outside fenced code blocks.
func Demote(md string) string {
	lines := strings.Split(md, "\n")
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if f := fenceOf(trimmed); f != "" {
			switch {
			case fence == "":
				fence = f
			case strings.HasPrefix(trimmed, fence):
				fence = ""
			}
			continue
		}
		if fence == "" && len(line)-len(trimmed) < 4 {
			lines[i] = line[:len(line)-len(trimmed)] + atx.ReplaceAllString(trimmed, "#$1$2")
		}
	}
	return strings.Join(lines, "\n")
}

func fenceOf(line string) string {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(line, f) {
			return f
		}
	}
	return ""
}

// Prepend inserts section into the file at path, after its top-level title,
// creating the file with a "# Changelog" title when it does not exist.
func Prepend(path, section string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return os.WriteFile(path, []byte(title+"\n"+section), 0o644)
	}
	if err != nil {
		return err
	}
	content := string(data)
	head, rest := splitTitle(content)
	out := head + section
	if rest = strings.TrimLeft(rest, "\n"); rest != "" {
		out += "\n" + rest
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out), info.Mode())
}

// splitTitle separates the top-level title and any intro paragraph before
// the first release heading from the releases that follow.
func splitTitle(content string) (string, string) {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "# ") {
		return title + "\n", content
	}
	for i := 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			head := strings.TrimRight(strings.Join(lines[:i], ""), "\n") + "\n\n"
			return head, strings.Join(lines[i:], "")
		}
	}
	return strings.TrimRight(content, "\n") + "\n\n", ""
}
