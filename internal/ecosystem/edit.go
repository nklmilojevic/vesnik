package ecosystem

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// errNotFound means the key path does not exist in the file.
var errNotFound = errors.New("key not found")

// jsonFrame is one open object or array while scanning JSON tokens.
type jsonFrame struct {
	object  bool
	wantKey bool
	key     string
}

// pathOf returns the object keys leading to the current value; arrays add an
// empty segment, so paths through arrays never match.
func pathOf(frames []*jsonFrame) []string {
	out := make([]string, len(frames))
	for i, f := range frames {
		if f.object {
			out[i] = f.key
		}
	}
	return out
}

// setJSON replaces the string value at path in a JSON document without
// touching anything else, so indentation and key order survive.
func setJSON(data []byte, path []string, value string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var frames []*jsonFrame
	// valueDone marks the enclosing object ready for its next key.
	valueDone := func() {
		if n := len(frames); n > 0 && frames[n-1].object {
			frames[n-1].wantKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil, errNotFound
		}
		if err != nil {
			return nil, err
		}
		var top *jsonFrame
		if n := len(frames); n > 0 {
			top = frames[n-1]
		}
		if top != nil && top.object && top.wantKey {
			if tok == json.Delim('}') {
				frames = frames[:len(frames)-1]
				valueDone()
				continue
			}
			top.key, top.wantKey = tok.(string), false
			continue
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			frames = append(frames, &jsonFrame{object: tok == json.Delim('{'), wantKey: true})
			continue
		case json.Delim(']'):
			frames = frames[:len(frames)-1]
			valueDone()
			continue
		}
		if s, ok := tok.(string); ok && top != nil && top.object && slices.Equal(pathOf(frames), path) {
			end := int(dec.InputOffset())
			quoted, _ := json.Marshal(s)
			start := bytes.LastIndex(data[:end], quoted)
			if start < 0 {
				return nil, fmt.Errorf("cannot locate value of %s", strings.Join(path, "."))
			}
			repl, _ := json.Marshal(value)
			return append(append(append([]byte{}, data[:start]...), repl...), data[start+len(quoted):]...), nil
		}
		valueDone()
	}
}

// getJSON returns the string at path, or errNotFound.
func getJSON(data []byte, path []string) (string, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	for _, k := range path {
		m, ok := doc.(map[string]any)
		if !ok {
			return "", errNotFound
		}
		if doc, ok = m[k]; !ok {
			return "", errNotFound
		}
	}
	s, ok := doc.(string)
	if !ok {
		return "", fmt.Errorf("%s is not a string", strings.Join(path, "."))
	}
	return s, nil
}

var tomlHeader = regexp.MustCompile(`^\s*\[\s*([^\[\]]+?)\s*\]\s*(#.*)?$`)

// setTOML replaces a string value at path in a TOML document by scanning
// lines: path a.b.c means key c in table [a.b], or key a.b.c at the root.
func setTOML(data []byte, path []string, value string) ([]byte, error) {
	table := strings.Join(path[:len(path)-1], ".")
	key := path[len(path)-1]
	lines := strings.SplitAfter(string(data), "\n")
	current := ""
	for i, line := range lines {
		if m := tomlHeader.FindStringSubmatch(line); m != nil {
			current = strings.ReplaceAll(m[1], " ", "")
			continue
		}
		want := key
		if current == "" && table != "" {
			want = strings.Join(path, ".")
		} else if current != table {
			continue
		}
		re := regexp.MustCompile(`^(\s*` + regexp.QuoteMeta(want) + `\s*=\s*")([^"]*)(".*)$`)
		if m := re.FindStringSubmatch(strings.TrimRight(line, "\r\n")); m != nil {
			nl := line[len(strings.TrimRight(line, "\r\n")):]
			lines[i] = m[1] + value + m[3] + nl
			return []byte(strings.Join(lines, "")), nil
		}
	}
	return nil, errNotFound
}

// setYAML replaces the scalar at path in a YAML document in place, using the
// node position so comments and formatting survive.
func setYAML(data []byte, path []string, value string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 {
		return nil, errNotFound
	}
	node := root.Content[0]
	for _, k := range path {
		if node.Kind != yaml.MappingNode {
			return nil, errNotFound
		}
		var next *yaml.Node
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == k {
				next = node.Content[i+1]
				break
			}
		}
		if next == nil {
			return nil, errNotFound
		}
		node = next
	}
	if node.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("%s is not a scalar", strings.Join(path, "."))
	}
	lines := strings.SplitAfter(string(data), "\n")
	li := node.Line - 1
	line := lines[li]
	col := node.Column - 1
	rest := line[col:]
	var old string
	switch node.Style {
	case yaml.DoubleQuotedStyle, yaml.SingleQuotedStyle:
		q := rest[:1]
		end := strings.Index(rest[1:], q)
		if end < 0 {
			return nil, fmt.Errorf("cannot locate value of %s", strings.Join(path, "."))
		}
		old = rest[:end+2]
		value = q + value + q
	default:
		if !strings.HasPrefix(rest, node.Value) {
			return nil, fmt.Errorf("cannot locate value of %s", strings.Join(path, "."))
		}
		old = node.Value
	}
	lines[li] = line[:col] + value + rest[len(old):]
	return []byte(strings.Join(lines, "")), nil
}

// setRegex replaces the version group in every match of pattern. The group
// is the one named v, or the first group when no group has that name.
func setRegex(data []byte, pattern, value string) ([]byte, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	group := re.SubexpIndex("v")
	if group < 0 {
		if re.NumSubexp() < 1 {
			return nil, fmt.Errorf("regex %q needs a capture group for the version", pattern)
		}
		group = 1
	}
	matches := re.FindAllSubmatchIndex(data, -1)
	if len(matches) == 0 {
		return nil, errNotFound
	}
	var out bytes.Buffer
	last := 0
	for _, m := range matches {
		start, end := m[2*group], m[2*group+1]
		if start < 0 {
			continue
		}
		out.Write(data[last:start])
		out.WriteString(value)
		last = end
	}
	out.Write(data[last:])
	return out.Bytes(), nil
}
