package panel

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"telesrv/internal/procctl"
)

// envRow is either a group header (fieldIdx < 0, not selectable/editable)
// or one field within that group -- a single flat, navigable list built
// from procctl.ReadEnvGroups()'s nested groups/fields, the same shape
// server-panel.py's EnvEditorScreen presented as collapsible sections.
type envRow struct {
	groupIdx int
	fieldIdx int // -1 for a group header row
}

type envState struct {
	loaded bool
	err    error

	groups []procctl.EnvGroup
	values map[string]string
	rows   []envRow
	cursor int

	editing bool
	input   textinput.Model

	message   string
	messageOK bool

	height int
}

func newEnvState(mgr *procctl.Manager) envState {
	groups, err := mgr.ReadEnvGroups()
	s := envState{loaded: true, err: err, groups: groups, values: map[string]string{}}
	for gi, g := range groups {
		s.rows = append(s.rows, envRow{groupIdx: gi, fieldIdx: -1})
		for fi, f := range g.Fields {
			s.rows = append(s.rows, envRow{groupIdx: gi, fieldIdx: fi})
			s.values[f.Key] = f.Value
		}
	}
	// Land the cursor on the first real field, not the first group header.
	for i, r := range s.rows {
		if r.fieldIdx >= 0 {
			s.cursor = i
			break
		}
	}
	ti := textinput.New()
	ti.CharLimit = 0
	s.input = ti
	return s
}

func (s envState) field(row envRow) procctl.EnvField {
	return s.groups[row.groupIdx].Fields[row.fieldIdx]
}

type envSavedMsg struct{ err error }

func (s envState) update(m Model, msg tea.Msg) (envState, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.height = msg.Height
		return s, nil

	case envSavedMsg:
		if msg.err != nil {
			s.message, s.messageOK = "Failed to save .env: "+errString(msg.err), false
		} else {
			s.message, s.messageOK = ".env saved.", true
		}
		return s, nil

	case tea.KeyMsg:
		if s.editing {
			return s.updateEditing(msg)
		}
		return s.updateBrowsing(m, msg)
	}
	return s, nil
}

func (s envState) updateEditing(msg tea.KeyMsg) (envState, tea.Cmd) {
	switch msg.String() {
	case "enter":
		row := s.rows[s.cursor]
		s.values[s.field(row).Key] = s.input.Value()
		s.editing = false
		s.input.Blur()
		return s, nil
	case "esc":
		s.editing = false
		s.input.Blur()
		return s, nil
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return s, cmd
}

func (s envState) updateBrowsing(m Model, msg tea.KeyMsg) (envState, tea.Cmd) {
	switch msg.String() {
	case "esc", "b":
		return s, switchTo(screenDashboard)
	case "up", "k":
		s.moveCursor(-1)
		return s, nil
	case "down", "j":
		s.moveCursor(1)
		return s, nil
	case "enter":
		row := s.rows[s.cursor]
		if row.fieldIdx < 0 {
			return s, nil
		}
		f := s.field(row)
		s.input.SetValue(s.values[f.Key])
		s.input.CursorEnd()
		if f.Sensitive {
			s.input.EchoMode = textinput.EchoPassword
		} else {
			s.input.EchoMode = textinput.EchoNormal
		}
		s.input.Focus()
		s.editing = true
		return s, textinput.Blink
	case "ctrl+s":
		values := make(map[string]string, len(s.values))
		for k, v := range s.values {
			values[k] = v
		}
		mgr := m.mgr
		return s, func() tea.Msg { return envSavedMsg{err: mgr.WriteEnvValues(values)} }
	}
	return s, nil
}

func (s *envState) moveCursor(delta int) {
	if len(s.rows) == 0 {
		return
	}
	i := s.cursor
	for {
		i += delta
		if i < 0 || i >= len(s.rows) {
			return
		}
		if s.rows[i].fieldIdx >= 0 {
			s.cursor = i
			return
		}
	}
}

func (s envState) view(m Model) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Configure .env"))
	b.WriteString("\n\n")

	if s.err != nil {
		b.WriteString(errBannerStyle.Render(errString(s.err)))
		return b.String()
	}
	if len(s.groups) == 0 {
		b.WriteString(dimStyle.Render("No .env.example found -- nothing to configure."))
		return b.String()
	}

	// A simple windowed view around the cursor rather than a full
	// scrollable viewport -- .env.example's groups are short enough
	// (single digits of fields each) that jumping by group is enough,
	// and it avoids a second nested scroll region inside the TUI.
	visible := s.height - 10
	if visible < 5 {
		visible = 5
	}
	start := 0
	if s.cursor > visible/2 {
		start = s.cursor - visible/2
	}
	end := start + visible
	if end > len(s.rows) {
		end = len(s.rows)
		start = end - visible
		if start < 0 {
			start = 0
		}
	}

	lastGroup := -1
	for i := start; i < end; i++ {
		row := s.rows[i]
		if row.fieldIdx < 0 {
			g := s.groups[row.groupIdx]
			b.WriteString("\n" + panelTitleStyle.Render(g.Title) + "\n")
			lastGroup = row.groupIdx
			continue
		}
		if row.groupIdx != lastGroup {
			g := s.groups[row.groupIdx]
			b.WriteString("\n" + panelTitleStyle.Render(g.Title) + "\n")
			lastGroup = row.groupIdx
		}
		f := s.field(row)
		cursor := "  "
		if i == s.cursor {
			cursor = menuSelStyle.Render("")
		}
		value := s.values[f.Key]
		display := value
		if f.Sensitive && value != "" {
			display = strings.Repeat("*", 8)
		}
		if display == "" {
			display = dimStyle.Render("(empty)")
		}
		if i == s.cursor && s.editing {
			b.WriteString(fmt.Sprintf("%s %-32s %s\n", cursor, f.Key, s.input.View()))
		} else {
			b.WriteString(fmt.Sprintf("%s %-32s %s\n", cursor, f.Key, display))
		}
		if i == s.cursor && f.Description != "" {
			b.WriteString("     " + dimStyle.Render(f.Description) + "\n")
		}
	}

	b.WriteString("\n")
	if s.message != "" {
		if s.messageOK {
			b.WriteString(okBannerStyle.Render(s.message))
		} else {
			b.WriteString(errBannerStyle.Render(s.message))
		}
		b.WriteString("\n")
	}
	if s.editing {
		b.WriteString(keyHintStyle.Render("enter save field · esc cancel"))
	} else {
		b.WriteString(keyHintStyle.Render("↑/↓ navigate · enter edit field · ctrl+s save .env · esc/b back"))
	}
	return b.String()
}
