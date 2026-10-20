// Package tui is the review screen: a human reads every package's notes and
// bump, and accepts, edits, regenerates, or aborts before anything is
// written.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/nklmilojevic/vesnik/internal/ai"
	"github.com/nklmilojevic/vesnik/internal/release"
)

// Catppuccin Latte for light terminals, Mocha for dark ones.
var (
	accent  = lipgloss.AdaptiveColor{Light: "#8839ef", Dark: "#cba6f7"}
	good    = lipgloss.AdaptiveColor{Light: "#40a02b", Dark: "#a6e3a1"}
	warn    = lipgloss.AdaptiveColor{Light: "#df8e1d", Dark: "#f9e2af"}
	bad     = lipgloss.AdaptiveColor{Light: "#d20f39", Dark: "#f38ba8"}
	muted   = lipgloss.AdaptiveColor{Light: "#8c8fa1", Dark: "#7f849c"}
	surface = lipgloss.AdaptiveColor{Light: "#ccd0da", Dark: "#45475a"}

	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(accent)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	mutedStyle    = lipgloss.NewStyle().Foreground(muted)
	warnStyle     = lipgloss.NewStyle().Foreground(warn)
	errStyle      = lipgloss.NewStyle().Foreground(bad)
	goodStyle     = lipgloss.NewStyle().Foreground(good)
	listStyle     = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderRight(true).BorderForeground(surface).PaddingRight(1)
)

const listWidth = 34

// Proposer regenerates one item.
type Proposer interface {
	Propose(ctx context.Context, it *release.Item) error
}

// Model is the review screen.
type Model struct {
	ctx      context.Context
	plan     *release.Plan
	proposer Proposer
	style    string

	cursor   int
	view     viewport.Model
	width    int
	height   int
	busy     map[*release.Item]bool
	status   string
	accepted bool
	quitting bool
}

// New builds the review screen. style is a glamour style name, such as
// "dark", "light" or "notty".
func New(ctx context.Context, plan *release.Plan, p Proposer, style string) *Model {
	m := &Model{ctx: ctx, plan: plan, proposer: p, style: style, busy: map[*release.Item]bool{}, width: 100, height: 30}
	m.view = viewport.New(m.width-listWidth-3, m.height-4)
	m.cursor = firstActive(plan)
	m.refresh()
	return m
}

func firstActive(plan *release.Plan) int {
	for i, it := range plan.Items {
		if !it.Skip {
			return i
		}
	}
	return 0
}

// Accepted reports whether the user accepted the plan.
func (m *Model) Accepted() bool { return m.accepted }

func (m *Model) Init() tea.Cmd { return nil }

type regenMsg struct {
	it  *release.Item
	p   ai.Proposal
	err error
}

type editedMsg struct {
	it   *release.Item
	path string
	err  error
}

func (m *Model) current() *release.Item { return m.plan.Items[m.cursor] }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.view.Width = max(20, msg.Width-listWidth-3)
		m.view.Height = max(5, msg.Height-4)
		m.refresh()
		return m, nil

	case regenMsg:
		delete(m.busy, msg.it)
		if msg.err != nil {
			m.status = errStyle.Render("regenerate failed: " + msg.err.Error())
		} else {
			msg.it.Proposal, msg.it.Notes, msg.it.AIError = msg.p, msg.p.Notes, nil
			if err := m.plan.SetBump(msg.it, msg.p.Bump); err != nil {
				m.status = errStyle.Render(err.Error())
			} else {
				m.status = goodStyle.Render("regenerated " + msg.it.Pkg.Name)
			}
		}
		m.refresh()
		return m, nil

	case editedMsg:
		defer func() { _ = os.Remove(msg.path) }()
		if msg.err != nil {
			m.status = errStyle.Render("editor: " + msg.err.Error())
			return m, nil
		}
		data, err := os.ReadFile(msg.path)
		switch notes := strings.TrimSpace(string(data)); {
		case err != nil:
			m.status = errStyle.Render(err.Error())
		case notes == "":
			m.status = warnStyle.Render("empty notes; kept the previous text")
		default:
			msg.it.Notes = notes
			m.status = goodStyle.Render("edited " + msg.it.Pkg.Name)
		}
		m.refresh()
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	it := m.current()
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.view.GotoTop()
		}
	case "down", "j":
		if m.cursor < len(m.plan.Items)-1 {
			m.cursor++
			m.view.GotoTop()
		}
	case "enter":
		if len(m.busy) > 0 {
			m.status = warnStyle.Render("wait for the model to finish")
			return m, nil
		}
		if len(m.plan.Active()) == 0 {
			m.status = warnStyle.Render("every package is skipped; nothing to release")
			return m, nil
		}
		m.accepted = true
		return m, tea.Quit
	case "b":
		if it.Skip || it.Next == nil {
			return m, nil
		}
		if err := m.plan.SetBump(it, it.Bump.Cycle()); err != nil {
			m.status = errStyle.Render(err.Error())
		} else {
			m.status = ""
		}
	case "s":
		switch {
		case it.Next == nil:
			m.status = warnStyle.Render(it.Pkg.Name + " has no changes to release")
		case it.Skip:
			it.Skip, it.Why = false, ""
		default:
			it.Skip, it.Why = true, "skipped in review"
		}
	case "r":
		if it.Next == nil || m.busy[it] {
			return m, nil
		}
		m.busy[it] = true
		m.status = mutedStyle.Render("asking the model about " + it.Pkg.Name + "…")
		m.refresh()
		return m, m.regenerate(it)
	case "e":
		if it.Next == nil || m.busy[it] {
			return m, nil
		}
		return m, m.edit(it)
	default:
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd
	}
	m.refresh()
	return m, nil
}

func (m *Model) regenerate(it *release.Item) tea.Cmd {
	// The model works on a copy so View never reads an item mid-update.
	tmp := *it
	return func() tea.Msg {
		err := m.proposer.Propose(m.ctx, &tmp)
		return regenMsg{it: it, p: tmp.Proposal, err: err}
	}
}

func (m *Model) edit(it *release.Item) tea.Cmd {
	f, err := os.CreateTemp("", "vesnik-"+it.Pkg.Name+"-*.md")
	if err != nil {
		return func() tea.Msg { return editedMsg{it: it, err: err} }
	}
	_, err = f.WriteString(it.Notes + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return func() tea.Msg { return editedMsg{it: it, path: f.Name(), err: err} }
	}
	cmd := exec.Command(Editor()[0], append(Editor()[1:], f.Name())...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return editedMsg{it: it, path: f.Name(), err: err} })
}

// Editor returns the user's editor command from VISUAL or EDITOR.
func Editor() []string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(env)); len(f) > 0 {
			return f
		}
	}
	return []string{"vi"}
}

func (m *Model) refresh() {
	it := m.current()
	var b strings.Builder
	if it.Skip && it.Next == nil {
		fmt.Fprintf(&b, "%s\n\n%s\n", titleStyle.Render(it.Pkg.Name), mutedStyle.Render("Skipped: "+it.Why))
		m.view.SetContent(b.String())
		return
	}
	from := "unreleased"
	if it.Current != nil {
		from = it.Current.String()
	}
	fmt.Fprintf(&b, "%s  %s → %s  %s\n", titleStyle.Render(it.Pkg.Name), from,
		selectedStyle.Render(it.Next.String()), mutedStyle.Render(string(it.Bump)+" bump, tag "+it.Tag))
	if it.Skip {
		b.WriteString(warnStyle.Render("Skipped in review; press s to include it again.") + "\n")
	}
	if w := it.Warning(); w != "" {
		b.WriteString(warnStyle.Render("Warning: "+w) + "\n")
	}
	if it.AIError != nil {
		msg := fmt.Sprintf("AI unavailable: %v. The release uses GitHub's generated notes; press r to retry or e to write notes.", it.AIError)
		b.WriteString(warnStyle.Render(wrap(msg, m.view.Width)) + "\n")
	}
	if r := it.Proposal.Reasoning; r != "" {
		b.WriteString(mutedStyle.Render(wrap("Why: "+r, m.view.Width)) + "\n")
	}
	summary := fmt.Sprintf("%d commits since %s · files: %s",
		len(it.Changes.Changes), or(it.Base, "the first commit"), strings.Join(it.Files(), ", "))
	b.WriteString(mutedStyle.Render(wrap(summary, m.view.Width)) + "\n")
	if m.busy[it] {
		b.WriteString("\n" + mutedStyle.Render("Regenerating…") + "\n")
	}
	b.WriteString(m.render(it.Notes))
	m.view.SetContent(b.String())
}

func (m *Model) render(md string) string {
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(m.style), glamour.WithWordWrap(max(20, m.view.Width-2)))
	if err != nil {
		return "\n" + md
	}
	out, err := r.Render(md)
	if err != nil {
		return "\n" + md
	}
	return out
}

func (m *Model) View() string {
	if m.quitting || m.accepted {
		return ""
	}
	var list strings.Builder
	list.WriteString(titleStyle.Render("vesnik release") + "\n\n")
	for i, it := range m.plan.Items {
		line := fmt.Sprintf("%-12s", truncate(it.Pkg.Name, 12))
		switch {
		case it.Skip:
			line = mutedStyle.Render(line + " skip")
		default:
			line += " " + it.Next.String()
			if it.Warning() != "" {
				line += warnStyle.Render(" !")
			}
			if m.busy[it] {
				line += mutedStyle.Render(" …")
			}
		}
		if i == m.cursor {
			line = selectedStyle.Render("▸ ") + line
		} else {
			line = "  " + line
		}
		list.WriteString(line + "\n")
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		listStyle.Width(listWidth).Height(m.view.Height).Render(list.String()),
		" "+m.view.View(),
	)
	help := mutedStyle.Render("enter accept · e edit · r regenerate · b bump · s skip · j/k move · pgup/pgdn scroll · q abort")
	return body + "\n" + m.status + "\n" + help
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func wrap(s string, width int) string {
	return lipgloss.NewStyle().Width(max(20, width)).Render(s)
}

// Run shows the review screen and reports whether the user accepted.
func Run(ctx context.Context, plan *release.Plan, p Proposer) (bool, error) {
	style := "light"
	if lipgloss.HasDarkBackground() {
		style = "dark"
	}
	m := New(ctx, plan, p, style)
	if _, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run(); err != nil {
		return false, err
	}
	return m.Accepted(), nil
}
