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
		if msg.err != nil {
			s.message = "Failed to set edition: " + errString(msg.err)
		} else {
			s.current = msg.edition
			s.saved = true
			s.message = "Edition set to " + procctl.DisplayEditionName(msg.edition) + ". Restart to apply it."
		}
		return s, nil

	case tea.KeyMsg:
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

func (s editionState) apply(m Model) (editionState, tea.Cmd) {
	choice := editionChoices[s.cursor].value
	mgr := m.mgr
	return s, func() tea.Msg { return editionSetMsg{edition: choice, err: mgr.SetEdition(choice)} }
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
		if s.saved {
			b.WriteString(okBannerStyle.Render(s.message))
		} else {
			b.WriteString(errBannerStyle.Render(s.message))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(keyHintStyle.Render("↑/↓ navigate · enter/1-2 select · esc/b back"))
	return b.String()
}
