package panel

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"telesrv/internal/procctl"
)

// logsState live-tails owpengram-server's current-run startup log --
// server-panel.py's LogTailScreen equivalent, minus the "pick which log
// file" step (there is only the one StartupLogTail already exposes).
type logsState struct {
	vp     viewport.Model
	lines  []string
	err    error
	follow bool // auto-scroll to the bottom as new lines arrive
	ready  bool
}

func newLogsState() logsState {
	return logsState{follow: true}
}

type logTickMsg time.Time

func logTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return logTickMsg(t) })
}

type logDataMsg struct {
	lines []string
	err   error
}

func (l logsState) refreshCmd(mgr *procctl.Manager) tea.Cmd {
	return tea.Batch(
		func() tea.Msg {
			lines, err := mgr.StartupLogTail()
			return logDataMsg{lines: lines, err: err}
		},
		logTick(),
	)
}

func (l logsState) update(m Model, msg tea.Msg) (logsState, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		headerHeight := 3
		footerHeight := 2
		vpHeight := msg.Height - headerHeight - footerHeight
		if vpHeight < 3 {
			vpHeight = 3
		}
		if !l.ready {
			l.vp = viewport.New(msg.Width, vpHeight)
			l.ready = true
		} else {
			l.vp.Width, l.vp.Height = msg.Width, vpHeight
		}
		l.vp.SetContent(strings.Join(l.lines, "\n"))
		return l, nil

	case logDataMsg:
		l.lines, l.err = msg.lines, msg.err
		if l.ready {
			atBottom := l.vp.AtBottom()
			l.vp.SetContent(strings.Join(l.lines, "\n"))
			if l.follow || atBottom {
				l.vp.GotoBottom()
			}
		}
		return l, nil

	case logTickMsg:
		return l, tea.Batch(
			func() tea.Msg {
				lines, err := m.mgr.StartupLogTail()
				return logDataMsg{lines: lines, err: err}
			},
			logTick(),
		)

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "b", "q":
			return l, switchTo(screenDashboard)
		case "g":
			l.vp.GotoTop()
			l.follow = false
			return l, nil
		case "G":
			l.vp.GotoBottom()
			l.follow = true
			return l, nil
		}
		var cmd tea.Cmd
		l.vp, cmd = l.vp.Update(msg)
		l.follow = l.vp.AtBottom()
		return l, cmd
	}
	return l, nil
}

func (l logsState) view(m Model) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Logs"))
	b.WriteString("\n\n")
	if l.err != nil {
		b.WriteString(errBannerStyle.Render(errString(l.err)))
		b.WriteString("\n")
	} else if len(l.lines) == 0 {
		b.WriteString(dimStyle.Render("(no log output yet)"))
	}
	if l.ready {
		b.WriteString(boxStyle.Width(l.vp.Width).Render(l.vp.View()))
		b.WriteString("\n")
	}
	status := "following"
	if !l.follow {
		status = "scrolled up -- press G to jump to the end"
	}
	b.WriteString("\n")
	b.WriteString(keyHintStyle.Render("↑/↓/pgup/pgdn scroll · g/G top/bottom (" + status + ") · esc/b back"))
	return b.String()
}
