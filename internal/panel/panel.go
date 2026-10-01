// Package panel is the interactive terminal control panel telesrv-ctl opens
// when run with no arguments from a real terminal -- the Go, dependency-
// light replacement for tui-panel/server-panel.py's Textual app. It covers
// a live dashboard, process start/stop/restart/update, and an edition
// switch -- built on Bubble Tea (github.com/charmbracelet/bubbletea) plus
// lipgloss for styling, so it still looks right over a plain SSH session,
// not just a modern local terminal. Log viewing and .env editing are
// deliberately not here; both stay better served by the admin web panel's
// own Services/Server Settings pages.
//
// Every action here goes through internal/procctl.Manager -- the same code
// path the admin web panel's Server Settings page and telesrv-ctl's plain
// non-interactive subcommands use. This package owns only the terminal UI;
// it has no process-control logic of its own to drift out of sync.
package panel

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telesrv/internal/procctl"
)

// Run starts the interactive panel and blocks until the user quits. ctx
// cancellation (e.g. Ctrl+C at the process level, SIGTERM) does not
// interrupt an in-flight action -- matches every other synchronous action
// in this codebase (see cmd/telesrv-ctl's own resolveEdition prompt).
func Run(ctx context.Context, m *procctl.Manager) error {
	model := newModel(ctx, m)
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

type screen int

const (
	screenDashboard screen = iota
	screenEdition
)

// Model is the single top-level Bubble Tea model. Bubble Tea has no
// built-in notion of "sub-screens" -- this holds one state struct per
// screen and Update/View dispatch on `current`, which is the idiomatic
// pattern for a multi-screen Bubble Tea app.
type Model struct {
	ctx context.Context
	mgr *procctl.Manager

	current screen
	width   int
	height  int

	dash dashboardState
	ed   editionState

	quitting bool
}

func newModel(ctx context.Context, mgr *procctl.Manager) Model {
	return Model{
		ctx:     ctx,
		mgr:     mgr,
		current: screenDashboard,
		dash:    newDashboardState(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.dash.refreshCmd(m.mgr),
		tickCmd(),
	)
}

// tickMsg drives the dashboard's periodic auto-refresh (process/Docker
// status, admin info) -- server-panel.py's MainScreen did the same with
// set_interval(3, self.refresh_status) etc.
type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}

	case tickMsg:
		var cmd tea.Cmd
		if m.current == screenDashboard {
			cmd = m.dash.refreshCmd(m.mgr)
		}
		return m, tea.Batch(cmd, tickCmd())

	case switchScreenMsg:
		m.current = msg.to
		switch msg.to {
		case screenEdition:
			m.ed = newEditionState(m.mgr)
			return m, nil
		case screenDashboard:
			return m, m.dash.refreshCmd(m.mgr)
		}
		return m, nil
	}

	var cmd tea.Cmd
	switch m.current {
	case screenDashboard:
		m.dash, cmd = m.dash.update(m, msg)
	case screenEdition:
		m.ed, cmd = m.ed.update(m, msg)
	}
	return m, cmd
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	switch m.current {
	case screenEdition:
		return m.ed.view(m)
	default:
		return m.dash.view(m)
	}
}

// switchScreenMsg navigates to another screen, (re)initializing its state
// on the way in -- see the handler in Update.
type switchScreenMsg struct{ to screen }

func switchTo(s screen) tea.Cmd {
	return func() tea.Msg { return switchScreenMsg{to: s} }
}

// errString renders an error the same short way everywhere in this
// package (status/message lines), instead of Go's default `%v` verbosity.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%s", err)
}
