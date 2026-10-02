package panel

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"telesrv/internal/procctl"
)

type editionState struct {
	current string // "" if unset
	cursor  int    // 0 = portable, 1 = classic
	message string
	saved   bool
	// busy is set while ChangeEdition runs. It both styles the message
	// neutrally (it is progress, not an outcome) and swallows keypresses:
	// that call stops the server, moves a whole database and restarts, so a
	// second one started on top of it would be dumping from a server the
	// first one is in the middle of shutting down.
	busy bool
}

var editionChoices = []struct {
	value string // as stored in .env (procctl.SetEdition argument)
	label string
	desc  string
}{
	{"portable", "Portable", "embedded PostgreSQL and local disk storage, no Docker at all"},
	{"standard", "Classic", "PostgreSQL and MinIO run in Docker"},
}

func newEditionState(mgr *procctl.Manager) editionState {
	s := editionState{}
	if raw, ok := mgr.Edition(); ok {
		s.current = raw
		for i, c := range editionChoices {
			if c.value == raw {
				s.cursor = i
			}
		}
	}
	return s
}

type editionSetMsg struct {
	edition string
	err     error
}

func (s editionState) update(m Model, msg tea.Msg) (editionState, tea.Cmd) {
	switch msg := msg.(type) {
	case editionSetMsg:
		s.busy = false
		if msg.err != nil {
			s.message = "Failed to switch edition: " + errString(msg.err)
		} else {
			s.current = msg.edition
			s.saved = true
			s.message = "Switched to " + procctl.DisplayEditionName(msg.edition) + ", data carried over, server restarted."
		}
		return s, nil

	case tea.KeyMsg:
		if s.busy {
			return s, nil
		}
		switch msg.String() {
		case "esc", "b":
			return s, switchTo(screenDashboard)
		case "up", "k":
			s.cursor = (s.cursor - 1 + len(editionChoices)) % len(editionChoices)
			return s, nil
		case "down", "j":
			s.cursor = (s.cursor + 1) % len(editionChoices)
			return s, nil
		case "1":
			s.cursor = 0
			return s.apply(m)
		case "2":
			s.cursor = 1
			return s.apply(m)
		case "enter":
			return s.apply(m)
		}
	}
	return s, nil
}

// apply switches edition through ChangeEdition, which moves the database to
// the other edition's PostgreSQL before persisting the choice -- without
// that, picking the other entry here pointed the server at an unrelated
// database and every signed-in client saw the server as unreachable.
//
// force stays false: the refusal it would override means both editions hold
// real sessions, and one keypress in a menu is the wrong place to resolve
// that. The error names `telesrv-ctl set-edition ... --force` for whoever
// genuinely wants it.
func (s editionState) apply(m Model) (editionState, tea.Cmd) {
	choice := editionChoices[s.cursor].value
	if choice == s.current {
		s.saved = false
		s.busy = false
		s.message = "Already running the " + procctl.DisplayEditionName(choice) + " edition."
		return s, nil
	}
	mgr := m.mgr
	ctx := m.ctx
	s.busy = true
	s.saved = false
	// Rendered before the command below starts, so the wait is explained:
	// stopping, dumping, restoring and rebuilding takes far longer than
	// anything else this panel does.
	s.message = "Switching to " + procctl.DisplayEditionName(choice) + ": moving the database over and restarting, this can take a while..."
	return s, func() tea.Msg {
		return editionSetMsg{edition: choice, err: mgr.ChangeEdition(ctx, choice, false, nil)}
	}
}

func (s editionState) view(m Model) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Change edition"))
	b.WriteString("\n\n")
	b.WriteString("How should this install get PostgreSQL and blob storage?\n\n")

	for i, c := range editionChoices {
		marker := "  "
		if c.value == s.current {
			marker = goodStyle.Render("● ")
		}
		line := fmt.Sprintf("%d) %s -- %s", i+1, c.label, c.desc)
		if i == s.cursor {
			b.WriteString(marker + menuSelStyle.Render("") + " " + panelTitleStyle.Render(line) + "\n")
		} else {
			b.WriteString(marker + menuItemStyle.Render(line) + "\n")
		}
	}

	b.WriteString("\n")
	if s.message != "" {
		switch {
		case s.busy:
			b.WriteString(panelTitleStyle.Render(s.message))
		case s.saved:
			b.WriteString(okBannerStyle.Render(s.message))
		default:
			b.WriteString(errBannerStyle.Render(s.message))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if s.busy {
		b.WriteString(keyHintStyle.Render("working..."))
	} else {
		b.WriteString(keyHintStyle.Render("↑/↓ navigate · enter/1-2 select · esc/b back"))
	}
	return b.String()
}
