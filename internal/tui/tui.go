// Package tui is the review screen: a human reads every package's notes and
// bump, and accepts, edits, regenerates, or aborts before anything is
// written.
package tui

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/nklmilojevic/vesnik/internal/ai"
	"github.com/nklmilojevic/vesnik/internal/release"
)

// styles holds the review screen's colours: Catppuccin Latte for light
// terminals, Mocha for dark ones.
type styles struct {
	title, selected, muted, warn, err, good, list lipgloss.Style
}

func newStyles(dark bool) styles {
	pick := lipgloss.LightDark(dark)
	c := func(l, d string) color.Color { return pick(lipgloss.Color(l), lipgloss.Color(d)) }
	accent := c("#8839ef", "#cba6f7")
	return styles{
		title:    lipgloss.NewStyle().Bold(true).Foreground(accent),
		selected: lipgloss.NewStyle().Bold(true).Foreground(accent),
		muted:    lipgloss.NewStyle().Foreground(c("#8c8fa1", "#7f849c")),
		warn:     lipgloss.NewStyle().Foreground(c("#df8e1d", "#f9e2af")),
		err:      lipgloss.NewStyle().Foreground(c("#d20f39", "#f38ba8")),
		good:     lipgloss.NewStyle().Foreground(c("#40a02b", "#a6e3a1")),
		list: lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderRight(true).
			BorderForeground(c("#ccd0da", "#45475a")).PaddingRight(1),
	}
}

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
	st       styles

	cursor   int
	view     viewport.Model
	width    int
	height   int
	busy     map[*release.Item]bool
	status   string
	accepted bool
	quitting bool

	// login signs the provider in again; it is set while the model fails
	// for want of a login, and r runs it before regenerating.
	login []string
}

// New builds the review screen. style is a glamour style name, such as
// "dark", "light" or "notty".
func New(ctx context.Context, plan *release.Plan, p Proposer, style string) *Model {
	m := &Model{ctx: ctx, plan: plan, proposer: p, style: style, st: newStyles(style == "dark"),
		busy: map[*release.Item]bool{}, width: 100, height: 30}
	m.view = viewport.New(viewport.WithWidth(m.width-listWidth-3), viewport.WithHeight(m.height-4))
	m.cursor = firstActive(plan)
	for _, it := range plan.Items {
		if l := ai.Login(it.AIError); l != nil {
			m.login = l
		}
	}
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

func (m *Model) Init() tea.Cmd { return tea.RequestBackgroundColor }

type regenMsg struct {
	it  *release.Item
	p   ai.Proposal
	err error
}

type loginMsg struct {
	it  *release.Item
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
	case tea.BackgroundColorMsg:
		m.st = newStyles(msg.IsDark())
		if m.style != "notty" {
			m.style = glamourStyle(msg.IsDark())
		}
		m.refresh()
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.view.SetWidth(max(20, msg.Width-listWidth-3))
		m.view.SetHeight(max(5, msg.Height-4))
		m.refresh()
		return m, nil

	case regenMsg:
		delete(m.busy, msg.it)
		if msg.err != nil {
			status := "regenerate failed: " + msg.err.Error()
			if l := ai.Login(msg.err); l != nil {
				m.login = l
				status += "; press r to sign in and retry"
			}
			m.status = m.st.err.Render(status)
		} else {
			msg.it.Proposal, msg.it.Notes, msg.it.AIError = msg.p, msg.p.Notes, nil
			if err := m.plan.SetBump(msg.it, m.plan.Cap(msg.p.Bump)); err != nil {
				m.status = m.st.err.Render(err.Error())
			} else {
				m.status = m.st.good.Render("regenerated " + msg.it.Pkg.Name)
			}
		}
		m.refresh()
		return m, nil

	case loginMsg:
		if msg.err != nil {
			delete(m.busy, msg.it)
			m.status = m.st.err.Render("sign-in failed: " + msg.err.Error())
			m.refresh()
			return m, nil
		}
		m.login = nil
		m.status = m.st.muted.Render("asking the model about " + msg.it.Pkg.Name + "…")
		m.refresh()
		return m, m.regenerate(msg.it)

	case editedMsg:
		defer func() { _ = os.Remove(msg.path) }()
		if msg.err != nil {
			m.status = m.st.err.Render("editor: " + msg.err.Error())
			return m, nil
		}
		data, err := os.ReadFile(msg.path)
		switch notes := strings.TrimSpace(string(data)); {
		case err != nil:
			m.status = m.st.err.Render(err.Error())
		case notes == "":
			m.status = m.st.warn.Render("empty notes; kept the previous text")
		default:
			msg.it.Notes = notes
			m.status = m.st.good.Render("edited " + msg.it.Pkg.Name)
		}
		m.refresh()
		return m, nil

	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
			m.status = m.st.warn.Render("wait for the model to finish")
			return m, nil
		}
		if len(m.plan.Active()) == 0 {
			m.status = m.st.warn.Render("every package is skipped; nothing to release")
			return m, nil
		}
		m.accepted = true
		return m, tea.Quit
	case "b":
		if it.Skip || it.Next == nil {
			return m, nil
		}
		if err := m.plan.SetBump(it, it.Bump.Cycle()); err != nil {
			m.status = m.st.err.Render(err.Error())
		} else {
			m.status = ""
		}
	case "s":
		switch {
		case it.Next == nil:
			m.status = m.st.warn.Render(it.Pkg.Name + " has no changes to release")
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
		if m.login != nil {
			cmd := exec.Command(m.login[0], m.login[1:]...)
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return loginMsg{it: it, err: err} })
		}
		m.status = m.st.muted.Render("asking the model about " + it.Pkg.Name + "…")
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
		fmt.Fprintf(&b, "%s\n\n%s\n", m.st.title.Render(it.Pkg.Name), m.st.muted.Render("Skipped: "+it.Why))
		m.view.SetContent(b.String())
		return
	}
	from := "unreleased"
	if it.Current != nil {
		from = it.Current.String()
	}
	fmt.Fprintf(&b, "%s  %s → %s  %s\n", m.st.title.Render(it.Pkg.Name), from,
		m.st.selected.Render(it.Next.String()), m.st.muted.Render(string(it.Bump)+" bump, tag "+it.Tag))
	if it.Skip {
		b.WriteString(m.st.warn.Render("Skipped in review; press s to include it again.") + "\n")
	}
	if w := it.Warning(); w != "" {
		b.WriteString(m.st.warn.Render("Warning: "+w) + "\n")
	}
	if it.AIError != nil {
		retry := "retry"
		if m.login != nil {
			retry = "sign in and retry"
		}
		msg := fmt.Sprintf("AI unavailable: %v. The release uses GitHub's generated notes; press r to %s or e to write notes.", it.AIError, retry)
		b.WriteString(m.st.warn.Render(wrap(msg, m.view.Width())) + "\n")
	}
	if r := it.Proposal.Reasoning; r != "" {
		b.WriteString(m.st.muted.Render(wrap("Why: "+r, m.view.Width())) + "\n")
	}
	summary := fmt.Sprintf("%d commits since %s · files: %s",
		len(it.Changes.Changes), or(it.Base, "the first commit"), strings.Join(it.Files(), ", "))
	b.WriteString(m.st.muted.Render(wrap(summary, m.view.Width())) + "\n")
	if m.busy[it] {
		b.WriteString("\n" + m.st.muted.Render("Regenerating…") + "\n")
	}
	b.WriteString(m.render(it.Notes))
	m.view.SetContent(b.String())
}

func (m *Model) render(md string) string {
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(m.style), glamour.WithWordWrap(max(20, m.view.Width()-2)))
	if err != nil {
		return "\n" + md
	}
	out, err := r.Render(md)
	if err != nil {
		return "\n" + md
	}
	return out
}

func (m *Model) View() tea.View {
	if m.quitting || m.accepted {
		// Stay on the alternate screen so the empty final frame is not drawn
		// on the user's terminal before Bubble Tea restores it.
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}
	var list strings.Builder
	list.WriteString(m.st.title.Render("vesnik release") + "\n\n")
	for i, it := range m.plan.Items {
		line := fmt.Sprintf("%-12s", truncate(it.Pkg.Name, 12))
		switch {
		case it.Skip:
			line = m.st.muted.Render(line + " skip")
		default:
			line += " " + it.Next.String()
			if it.Warning() != "" {
				line += m.st.warn.Render(" !")
			}
			if m.busy[it] {
				line += m.st.muted.Render(" …")
			}
		}
		if i == m.cursor {
			line = m.st.selected.Render("▸ ") + line
		} else {
			line = "  " + line
		}
		list.WriteString(line + "\n")
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		m.st.list.Width(listWidth).Height(m.view.Height()).Render(list.String()),
		" "+m.view.View(),
	)
	help := m.st.muted.Render("enter accept · e edit · r regenerate · b bump · s skip · j/k move · pgup/pgdn scroll · q abort")
	v := tea.NewView(body + "\n" + m.status + "\n" + help)
	v.AltScreen = true
	return v
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

func glamourStyle(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}

// Run shows the review screen and reports whether the user accepted.
func Run(ctx context.Context, plan *release.Plan, p Proposer) (bool, error) {
	m := New(ctx, plan, p, glamourStyle(lipgloss.HasDarkBackground(os.Stdin, os.Stdout)))
	if _, err := tea.NewProgram(m, tea.WithContext(ctx)).Run(); err != nil {
		return false, err
	}
	return m.Accepted(), nil
}
