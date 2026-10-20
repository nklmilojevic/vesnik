package tui

import (
	"context"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Masterminds/semver/v3"

	"github.com/nklmilojevic/vesnik/internal/ai"
	"github.com/nklmilojevic/vesnik/internal/config"
	"github.com/nklmilojevic/vesnik/internal/ecosystem"
	"github.com/nklmilojevic/vesnik/internal/release"
	"github.com/nklmilojevic/vesnik/internal/version"
)

type fakeProposer struct {
	p   ai.Proposal
	err error
}

func (f fakeProposer) Propose(_ context.Context, it *release.Item) error {
	if f.err != nil {
		return f.err
	}
	it.Proposal, it.Notes = f.p, f.p.Notes
	return nil
}

func item(t *testing.T, name, cur string, b version.Bump) *release.Item {
	t.Helper()
	tmpl, _ := version.ParseTemplate(name + "@v{version}")
	it := &release.Item{
		Pkg: config.Package{Name: name, Path: name}, Template: tmpl, Bumper: ecosystem.None{},
		Current: semver.MustParse(cur), LastFinal: semver.MustParse(cur),
		Proposal:  ai.Proposal{Bump: b, Reasoning: "why", Notes: "## Fixes\n\n- " + name},
		Notes:     "## Fixes\n\n- " + name,
		Changelog: name + "/CHANGELOG.md",
	}
	return it
}

func newModel(t *testing.T, p Proposer) (*Model, *release.Plan) {
	t.Helper()
	api := item(t, "api", "1.0.0", version.Patch)
	web := &release.Item{Pkg: config.Package{Name: "web"}, Skip: true, Why: "no changes since web@v1.0.0"}
	plan := release.NewPlan(version.Options{}, web, api)
	if err := plan.SetBump(api, version.Patch); err != nil {
		t.Fatal(err)
	}
	return New(context.Background(), plan, p, "notty"), plan
}

func press(m *Model, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEsc}
		default:
			msg = tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		}
		_, cmd = m.Update(msg)
	}
	return cmd
}

func TestStartsOnFirstActiveAndAccepts(t *testing.T) {
	m, _ := newModel(t, fakeProposer{})
	if m.current().Pkg.Name != "api" {
		t.Fatalf("cursor on %s", m.current().Pkg.Name)
	}
	if !strings.Contains(m.View().Content, "api@v1.0.1") && !strings.Contains(m.view.View(), "api@v1.0.1") {
		t.Fatalf("view lacks the tag:\n%s", m.View().Content)
	}
	if cmd := press(m, "enter"); cmd == nil || !m.Accepted() {
		t.Fatal("enter did not accept")
	}
	if !m.View().AltScreen {
		t.Fatal("final view left the alternate screen")
	}
}

func TestAbort(t *testing.T) {
	m, _ := newModel(t, fakeProposer{})
	if cmd := press(m, "q"); cmd == nil || m.Accepted() {
		t.Fatal("q did not abort")
	}
	if !m.View().AltScreen {
		t.Fatal("final view left the alternate screen")
	}
}

func TestBackgroundColorRestyles(t *testing.T) {
	m, _ := newModel(t, fakeProposer{})
	m.style = "light"
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init does not ask for the background colour")
	}
	m.Update(tea.BackgroundColorMsg{Color: color.Black})
	if m.style != "dark" || m.st.title.GetForeground() != newStyles(true).title.GetForeground() {
		t.Fatalf("dark background left style %q", m.style)
	}
	m.Update(tea.BackgroundColorMsg{Color: color.White})
	if m.style != "light" || m.st.title.GetForeground() != newStyles(false).title.GetForeground() {
		t.Fatalf("light background left style %q", m.style)
	}
}

func TestBumpCyclesAndUpdatesTag(t *testing.T) {
	m, _ := newModel(t, fakeProposer{})
	press(m, "b")
	if it := m.current(); it.Bump != version.Minor || it.Tag != "api@v1.1.0" {
		t.Fatalf("after b: %s %s", it.Bump, it.Tag)
	}
	press(m, "b", "b")
	if it := m.current(); it.Bump != version.Patch || it.Tag != "api@v1.0.1" {
		t.Fatalf("after three b: %s %s", it.Bump, it.Tag)
	}
}

func TestSkipEverythingBlocksAccept(t *testing.T) {
	m, plan := newModel(t, fakeProposer{})
	press(m, "s")
	if len(plan.Active()) != 0 {
		t.Fatal("s did not skip")
	}
	press(m, "enter")
	if m.Accepted() || !strings.Contains(m.status, "nothing to release") {
		t.Fatalf("accepted with everything skipped; status %q", m.status)
	}
	press(m, "s")
	if len(plan.Active()) != 1 {
		t.Fatal("s did not include the package again")
	}
	press(m, "k", "s")
	if !strings.Contains(m.status, "no changes") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestRegenerate(t *testing.T) {
	m, _ := newModel(t, fakeProposer{p: ai.Proposal{Bump: version.Major, Reasoning: "removed", Notes: "## Other changes\n\n- Gone."}})
	cmd := press(m, "r")
	if cmd == nil || !m.busy[m.current()] {
		t.Fatal("r did not start regenerating")
	}
	press(m, "enter")
	if m.Accepted() {
		t.Fatal("accepted while the model was busy")
	}
	m.Update(cmd())
	it := m.current()
	if m.busy[it] || it.Notes != "## Other changes\n\n- Gone." || it.Tag != "api@v2.0.0" {
		t.Fatalf("after regenerate: busy %v, notes %q, tag %s", m.busy[it], it.Notes, it.Tag)
	}

	m, _ = newModel(t, fakeProposer{err: errors.New("rate limited")})
	cmd = press(m, "r")
	m.Update(cmd())
	if !strings.Contains(m.status, "rate limited") || m.current().Notes != "## Fixes\n\n- api" {
		t.Fatalf("failed regenerate: status %q, notes %q", m.status, m.current().Notes)
	}
}

func TestEditReadsTheFileBack(t *testing.T) {
	m, _ := newModel(t, fakeProposer{})
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("\nEdited notes.\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Update(editedMsg{it: m.current(), path: path})
	if m.current().Notes != "Edited notes." {
		t.Fatalf("notes = %q", m.current().Notes)
	}
	empty := filepath.Join(t.TempDir(), "empty.md")
	_ = os.WriteFile(empty, []byte("  \n"), 0o600)
	m.Update(editedMsg{it: m.current(), path: empty})
	if m.current().Notes != "Edited notes." || !strings.Contains(m.status, "empty notes") {
		t.Fatalf("empty edit: notes %q status %q", m.current().Notes, m.status)
	}
}

func TestEditorFromEnv(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code --wait")
	if got := Editor(); len(got) != 2 || got[0] != "code" || got[1] != "--wait" {
		t.Fatalf("Editor = %v", got)
	}
	t.Setenv("EDITOR", "")
	if got := Editor(); got[0] != "vi" {
		t.Fatalf("Editor = %v", got)
	}
}

func TestAIUnavailableWarningClearsAfterRegenerate(t *testing.T) {
	m, _ := newModel(t, fakeProposer{p: ai.Proposal{Bump: version.Patch, Notes: "## Fixes\n\n- Back."}})
	m.current().AIError = errors.New("claude is not on PATH")
	m.refresh()
	if !strings.Contains(m.view.View(), "AI unavailable") {
		t.Fatalf("view lacks the warning:\n%s", m.view.View())
	}
	cmd := press(m, "r")
	m.Update(cmd())
	if m.current().AIError != nil || strings.Contains(m.view.View(), "AI unavailable") {
		t.Fatal("warning stayed after a successful regenerate")
	}
}

func TestRegenerateRespectsMaxBump(t *testing.T) {
	m, plan := newModel(t, fakeProposer{p: ai.Proposal{Bump: version.Major, Notes: "## Other changes\n\n- Gone."}})
	plan.MaxBump = version.Minor
	cmd := press(m, "r")
	m.Update(cmd())
	if it := m.current(); it.Bump != version.Minor || it.Tag != "api@v1.1.0" {
		t.Fatalf("after regenerate: %s %s", it.Bump, it.Tag)
	}
}
