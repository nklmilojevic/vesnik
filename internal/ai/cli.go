package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Claude runs the claude CLI in print mode with no tools, no settings and no
// session, so it uses the user's Claude login and nothing else.
type Claude struct {
	Bin   string
	Model string
}

const (
	claudeName = "claude"
	codexName  = "codex"
)

func (c *Claude) Name() string { return claudeName }

func (c *Claude) JSON(ctx context.Context, req Request) ([]byte, error) {
	args := []string{
		"-p",
		"--tools", "",
		"--disable-slash-commands",
		"--no-session-persistence",
		"--setting-sources", "",
		"--output-format", "json",
		"--json-schema", req.Schema,
		"--system-prompt", req.System,
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	out, runErr := run(ctx, c.Bin, "", req.User, args...)
	var res struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	// claude exits non-zero on errors but still prints its JSON result;
	// its message is the useful part.
	if err := json.Unmarshal(out, &res); err != nil {
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("claude returned unexpected output: %w", err)
	}
	if res.IsError {
		err := fmt.Errorf("claude: %s", res.Result)
		if loggedOut(res.Result) {
			return nil, &AuthError{Err: err, Login: []string{c.Bin, "auth", "login"}}
		}
		return nil, err
	}
	if runErr != nil {
		return nil, runErr
	}
	if len(res.StructuredOutput) == 0 || string(res.StructuredOutput) == "null" {
		return []byte(res.Result), nil
	}
	return res.StructuredOutput, nil
}

// loggedOut reports whether a claude error asks the user to sign in again,
// such as an expired OAuth session or a missing login.
func loggedOut(msg string) bool {
	for _, s := range []string{"Failed to authenticate", "Not logged in", "/login", "auth login"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// Codex runs `codex exec` read-only in an empty directory, with the output
// constrained by --output-schema.
type Codex struct {
	Bin   string
	Model string
}

func (c *Codex) Name() string { return codexName }

func (c *Codex) JSON(ctx context.Context, req Request) ([]byte, error) {
	dir, err := os.MkdirTemp("", "vesnik-codex-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	schema := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schema, []byte(req.Schema), 0o600); err != nil {
		return nil, err
	}
	last := filepath.Join(dir, "last.json")
	args := []string{
		"exec",
		"--sandbox", "read-only",
		"--ephemeral",
		"--skip-git-repo-check",
		"--ignore-rules",
		"--color", "never",
		"--output-schema", schema,
		"--output-last-message", last,
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	args = append(args, "-")
	// codex has no system prompt flag; the instructions lead the prompt.
	prompt := req.System + "\n\n---\n\n" + req.User
	if _, err := run(ctx, c.Bin, dir, prompt, args...); err != nil {
		return nil, err
	}
	out, err := os.ReadFile(last)
	if err != nil {
		return nil, fmt.Errorf("codex wrote no answer: %w", err)
	}
	return out, nil
}

func run(ctx context.Context, bin, dir, stdin string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(bin); err != nil {
		return nil, fmt.Errorf("%s is not on PATH; install it or pick another provider", bin)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := lastLine(stderr.String())
		if msg == "" {
			msg = lastLine(stdout.String())
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		return stdout.Bytes(), fmt.Errorf("%s: %v: %s", bin, err, msg)
	}
	return stdout.Bytes(), nil
}

// lastLine returns the last non-empty line of a CLI's output, shortened.
// CLIs print banners and progress first and the error last.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(line) > 300 {
		line = line[:300] + "…"
	}
	return line
}
