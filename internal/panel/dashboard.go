package panel

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"telesrv/internal/procctl"
)

var dashboardMenu = []struct {
	label string
	hint  string
}{
	{"Start", ""},
	{"Stop", ""},
	{"Restart", "rebuild and relaunch"},
	{"Update", "git pull --ff-only, rebuild, relaunch"},
	{"Logs", "view the current run's startup log"},
	{"Configure .env", ""},
	{"Change edition", "portable / classic"},
	{"Quit", "server keeps running"},
}

const (
	menuStart = iota
	menuStop
	menuRestart
	menuUpdate
	menuLogs
	menuEnv
	menuEdition
	menuQuit
)

type dashboardState struct {
	loaded bool

	status      procctl.Status
	docker      []procctl.DockerService
	dockerErr   error
	editionText string

	adminURL      string
	adminURLOk    bool
	adminPassword string
	address       string
	addressOk     bool
	pubkey        string
	pubkeyOk      bool

	cursor int

	busy      bool
	busyLabel string
	message   string
	messageOK bool
}

func newDashboardState() dashboardState {
	return dashboardState{editionText: "..."}
}

// dashboardDataMsg is the result of a background refresh -- see refreshCmd.
type dashboardDataMsg dashboardState

func (d dashboardState) refreshCmd(mgr *procctl.Manager) tea.Cmd {
	return func() tea.Msg {
		next := dashboardState{loaded: true}
		next.status = mgr.Status()
		next.docker, next.dockerErr = mgr.DockerStatus(context.Background())

		if raw, ok := mgr.Edition(); ok {
			next.editionText = procctl.DisplayEditionName(raw)
		} else {
			next.editionText = "not set"
		}

		groups, _ := mgr.ReadEnvGroups()
		next.adminURL, next.adminURLOk = procctl.AdminUIURL(groups)
		next.adminPassword = procctl.EnvGroupValue(groups, "TELESRV_ADMIN_UI_PASSWORD")
		next.address, next.addressOk = mgr.ServerAddress()
		next.pubkey, next.pubkeyOk = mgr.ServerPublicKeyPEM()
		return dashboardDataMsg(next)
	}
}

// actionDoneMsg is the result of a Start/Stop/Restart/Update triggered
// from the menu -- these can take real time (a `go build`, `docker
// compose up`, ...), so they run as a tea.Cmd instead of blocking Update.
type actionDoneMsg struct {
	label string
	log   string
	err   error
}

func runAction(ctx context.Context, mgr *procctl.Manager, label string, fn func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		log, err := fn()
		return actionDoneMsg{label: label, log: log, err: err}
	}
}

type copyDoneMsg struct {
	label string
	ok    bool
}

func copyCmd(label, value string) tea.Cmd {
	return func() tea.Msg {
		if copyToClipboard(value) {
			return copyDoneMsg{label: label, ok: true}
		}
		// No local clipboard tool worked -- typically a headless remote
		// server reached over SSH, same fallback server-panel.py used.
		copyToClipboardOSC52(value)
		return copyDoneMsg{label: label, ok: true}
	}
}

func (d dashboardState) update(m Model, msg tea.Msg) (dashboardState, tea.Cmd) {
	switch msg := msg.(type) {
	case dashboardDataMsg:
		next := dashboardState(msg)
		next.cursor = d.cursor
		next.busy = d.busy
		next.busyLabel = d.busyLabel
		next.message = d.message
		next.messageOK = d.messageOK
		return next, nil

	case actionDoneMsg:
		d.busy = false
		d.busyLabel = ""
		if msg.err != nil {
			d.message = msg.label + " failed: " + errString(msg.err)
			d.messageOK = false
		} else {
			d.message = msg.label + " done."
			if summary := lastNonEmptyLine(msg.log); summary != "" {
				d.message = msg.label + ": " + summary
			}
			d.messageOK = true
		}
		return d, d.refreshCmd(m.mgr)

	case copyDoneMsg:
		d.message = msg.label + " copied to clipboard."
		d.messageOK = true
		return d, nil

	case tea.KeyMsg:
		if d.busy {
			return d, nil
		}
		switch msg.String() {
		case "up", "k":
			d.cursor = (d.cursor - 1 + len(dashboardMenu)) % len(dashboardMenu)
			return d, nil
		case "down", "j":
			d.cursor = (d.cursor + 1) % len(dashboardMenu)
			return d, nil
		case "1", "2", "3", "4", "5", "6", "7", "8":
			d.cursor = int(msg.String()[0] - '1')
			return d.trigger(m)
		case "a":
			if d.addressOk {
				return d, copyCmd("Address", d.address)
			}
			return d, nil
		case "p":
			if d.pubkeyOk {
				return d, copyCmd("Public key", d.pubkey)
			}
			return d, nil
		case "w":
			if d.adminPassword != "" {
				return d, copyCmd("Password", d.adminPassword)
			}
			return d, nil
		case "enter":
			return d.trigger(m)
		case "q":
			return d, tea.Quit
		}
	}
	return d, nil
}

func (d dashboardState) trigger(m Model) (dashboardState, tea.Cmd) {
	switch d.cursor {
	case menuStart:
		d.busy, d.busyLabel = true, "Starting"
		return d, runAction(m.ctx, m.mgr, "Start", func() (string, error) { return m.mgr.Start(m.ctx) })
	case menuStop:
		d.busy, d.busyLabel = true, "Stopping"
		return d, runAction(m.ctx, m.mgr, "Stop", func() (string, error) { return m.mgr.Stop(), nil })
	case menuRestart:
		d.busy, d.busyLabel = true, "Restarting"
		return d, runAction(m.ctx, m.mgr, "Restart", func() (string, error) { return m.mgr.Restart(m.ctx) })
	case menuUpdate:
		d.busy, d.busyLabel = true, "Updating"
		return d, runAction(m.ctx, m.mgr, "Update", func() (string, error) { return m.mgr.Update(m.ctx) })
	case menuLogs:
		return d, switchTo(screenLogs)
	case menuEnv:
		return d, switchTo(screenEnv)
	case menuEdition:
		return d, switchTo(screenEdition)
	case menuQuit:
		return d, tea.Quit
	}
	return d, nil
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func (d dashboardState) view(m Model) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("OwpenGram Control Panel"))
	b.WriteString("\n\n")

	if !d.loaded {
		b.WriteString(dimStyle.Render("Loading..."))
		return b.String()
	}

	b.WriteString(panelTitleStyle.Render("Services"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s owpengram-server       %s\n", dot(d.status.ServerAlive), aliveText(d.status.ServerAlive, d.status.ServerPID)))
	b.WriteString(fmt.Sprintf("  %s owpengram-admin-panel  %s\n", dot(d.status.AdminAlive), aliveText(d.status.AdminAlive, d.status.AdminPID)))
	if d.dockerErr != nil {
		b.WriteString("  " + warnStyle.Render("docker: "+errString(d.dockerErr)) + "\n")
	}
	for _, svc := range d.docker {
		health := svc.Health
		if health == "" {
			health = "-"
		}
		b.WriteString(fmt.Sprintf("  %s docker/%-10s state=%-10s health=%s\n", dot(svc.State == "running"), svc.Name, svc.State, health))
	}
	b.WriteString("  edition: " + panelTitleStyle.Render(d.editionText) + "\n\n")

	b.WriteString(panelTitleStyle.Render("Server"))
	b.WriteString("\n")
	b.WriteString("  " + copyLine("Address", d.addressOk, "a") + "\n")
	b.WriteString("  " + copyLine("Public key", d.pubkeyOk, "p") + "\n")
	if d.adminURLOk {
		b.WriteString("  Admin UI:   " + d.adminURL + "\n")
	} else {
		b.WriteString("  Admin UI:   " + dimStyle.Render("not configured") + "\n")
	}
	b.WriteString("  " + copyLine("Password", d.adminPassword != "", "w") + "\n\n")

	b.WriteString(panelTitleStyle.Render("Menu"))
	b.WriteString("\n")
	for i, item := range dashboardMenu {
		line := fmt.Sprintf("%d) %s", i+1, item.label)
		if item.hint != "" {
			line += "  " + dimStyle.Render("("+item.hint+")")
		}
		if i == d.cursor {
			b.WriteString(menuSelStyle.Render("") + " " + panelTitleStyle.Render(line) + "\n")
		} else {
			b.WriteString(menuItemStyle.Render(line) + "\n")
		}
	}

	b.WriteString("\n")
	if d.busy {
		b.WriteString(warnStyle.Render(d.busyLabel + "..."))
	} else if d.message != "" {
		if d.messageOK {
			b.WriteString(okBannerStyle.Render(d.message))
		} else {
			b.WriteString(errBannerStyle.Render(d.message))
		}
	}
	b.WriteString("\n\n")
	b.WriteString(keyHintStyle.Render("↑/↓ navigate · enter/1-8 select · a/p/w copy address/key/password · q quit"))
	return b.String()
}

func aliveText(alive bool, pid int) string {
	if alive {
		return goodStyle.Render(fmt.Sprintf("running (pid=%d)", pid))
	}
	return dimStyle.Render("stopped")
}

// copyLine renders a "Label: (press X to copy)" line -- the value itself
// is deliberately never shown on screen, only a hint that it can be
// copied (matches tui-panel/server-panel.py's CopyButton, which never
// rendered the address/key/password it copied either).
func copyLine(label string, available bool, key string) string {
	if !available {
		return fmt.Sprintf("%-11s %s", label+":", dimStyle.Render("not available"))
	}
	return fmt.Sprintf("%-11s %s", label+":", dimStyle.Render("(press "+key+" to copy)"))
}
