package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/nklmilojevic/vesnik/internal/version"
)

// fakeBin writes an executable shell script and returns its path.
func fakeBin(t *testing.T, name, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

var req = Request{System: "be brief", User: "commits: fix: a"}

func TestParse(t *testing.T) {
	if _, err := parse([]byte(`{"bump":"huge","reasoning":"","notes":"x"}`)); err == nil {
		t.Fatal("want error for unknown bump")
	}
	if _, err := parse([]byte(`{"bump":"patch","reasoning":"","notes":"  "}`)); err == nil {
		t.Fatal("want error for empty notes")
	}
	p, err := parse([]byte(`{"bump":"minor","reasoning":" r ","notes":"\n## Fixes\n"}`))
	if err != nil || p.Bump != version.Minor || p.Notes != "## Fixes" || p.Reasoning != "r" {
		t.Fatalf("parse = %+v, %v", p, err)
	}
}

func TestClaude(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	bin := fakeBin(t, "claude", `printf '%s\n' "$@" > `+argsFile+`
input=$(cat)
case "$input" in *"fix: a"*) ;; *) echo "prompt not on stdin" >&2; exit 1;; esac
printf '%s\n' '{"type":"result","is_error":false,"result":"","structured_output":{"bump":"patch","reasoning":"only fixes","notes":"## Fixes\n\n- A fix."}}'
`)
	p, err := Propose(context.Background(), &Claude{Bin: bin, Model: "claude-sonnet-5-5"}, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Bump != version.Patch || p.Notes != "## Fixes\n\n- A fix." {
		t.Fatalf("proposal = %+v", p)
	}
	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"-p", "--json-schema", "--no-session-persistence", "--system-prompt\nbe brief", "--model\nclaude-sonnet-5-5"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args lack %q:\n%s", want, args)
		}
	}
}

func TestClaudeError(t *testing.T) {
	bin := fakeBin(t, "claude", `cat >/dev/null; echo '{"is_error":true,"result":"Not logged in"}'`)
	_, err := Propose(context.Background(), &Claude{Bin: bin}, req)
	if err == nil || !strings.Contains(err.Error(), "Not logged in") {
		t.Fatalf("err = %v", err)
	}
	failing := fakeBin(t, "claude", `echo "boom" >&2; exit 2`)
	if _, err := Propose(context.Background(), &Claude{Bin: failing}, req); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodex(t *testing.T) {
	bin := fakeBin(t, "codex", `
out=""; schema=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output-last-message) out="$2"; shift;;
    --output-schema) schema="$2"; shift;;
    --sandbox) [ "$2" = "read-only" ] || { echo "sandbox not read-only" >&2; exit 1; }; shift;;
  esac
  shift
done
grep -q '"bump"' "$schema" || { echo "no schema" >&2; exit 1; }
input=$(cat)
case "$input" in "be brief"*"commits: fix: a"*) ;; *) echo "bad prompt" >&2; exit 1;; esac
printf '%s\n' '{"bump":"minor","reasoning":"new flag","notes":"## New features\n\n- A flag."}' > "$out"
`)
	p, err := Propose(context.Background(), &Codex{Bin: bin}, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Bump != version.Minor || !strings.Contains(p.Notes, "A flag.") {
		t.Fatalf("proposal = %+v", p)
	}
}

func TestMissingBinary(t *testing.T) {
	_, err := Propose(context.Background(), &Claude{Bin: "vesnik-no-such-binary"}, req)
	if err == nil || !strings.Contains(err.Error(), "not on PATH") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnthropic(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if !strings.Contains(r.Header.Get("anthropic-beta"), "server-side-fallback-2026-07-01") {
			t.Errorf("missing fallback beta header: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5",
			"content":[{"type":"text","text":"{\"bump\":\"major\",\"reasoning\":\"removed flag\",\"notes\":\"## Other changes\\n\\n- Removed.\"}"}],
			"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()
	a := NewAnthropic("claude-sonnet-5-5", option.WithBaseURL(srv.URL), option.WithAPIKey("k"), option.WithMaxRetries(0))
	p, err := Propose(context.Background(), a, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Bump != version.Major || p.Reasoning != "removed flag" {
		t.Fatalf("proposal = %+v", p)
	}
	if body["model"] != "claude-sonnet-5-5" || body["fallbacks"] != "default" {
		t.Fatalf("request body = %v", body)
	}
	format := body["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Fatalf("output_config = %v", body["output_config"])
	}
}

func TestAnthropicRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"m","type":"message","role":"assistant","model":"m","content":[],"stop_reason":"refusal","usage":{"input_tokens":1,"output_tokens":0}}`)
	}))
	defer srv.Close()
	a := NewAnthropic("m", option.WithBaseURL(srv.URL), option.WithAPIKey("k"), option.WithMaxRetries(0))
	if _, err := Propose(context.Background(), a, req); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("err = %v", err)
	}
}

func TestClaudeErrorOnNonZeroExitShowsResult(t *testing.T) {
	bin := fakeBin(t, "claude", `cat >/dev/null; printf '%s\n' '{"is_error":true,"result":"Prompt is too long","usage":{}}'; exit 1`)
	_, err := Propose(context.Background(), &Claude{Bin: bin}, req)
	if err == nil || err.Error() != "claude: Prompt is too long" {
		t.Fatalf("err = %v", err)
	}
}
