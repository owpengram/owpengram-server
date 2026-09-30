package panel

import "github.com/charmbracelet/lipgloss"

// Palette. Kept to 256-color-safe ANSI approximations (no true-color
// requirement) so this still looks right over a plain SSH session in an
// old terminal, not just a modern local one.
var (
	colorAccent  = lipgloss.Color("39")  // blue
	colorGood    = lipgloss.Color("42")  // green
	colorBad     = lipgloss.Color("203") // red
	colorWarn    = lipgloss.Color("214") // orange
	colorDim     = lipgloss.Color("243") // grey
	colorBorder  = lipgloss.Color("238")
	colorHighlt  = lipgloss.Color("236") // selected-row background
	colorTitleBg = lipgloss.Color("24")
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255")).
			Background(colorTitleBg).
			Padding(0, 2)

	subtitleStyle = lipgloss.NewStyle().Foreground(colorDim)

	panelTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	goodStyle = lipgloss.NewStyle().Foreground(colorGood).Bold(true)
	badStyle  = lipgloss.NewStyle().Foreground(colorBad).Bold(true)
	warnStyle = lipgloss.NewStyle().Foreground(colorWarn).Bold(true)
	dimStyle  = lipgloss.NewStyle().Foreground(colorDim)

	menuItemStyle = lipgloss.NewStyle().PaddingLeft(2)
	menuSelStyle  = lipgloss.NewStyle().
			PaddingLeft(1).
			Bold(true).
			Foreground(lipgloss.Color("255")).
			Background(colorHighlt).
			SetString(" ›")

	keyHintStyle = lipgloss.NewStyle().Foreground(colorDim)

	errBannerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(colorBad).
			Padding(0, 1)

	okBannerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(colorGood).
			Padding(0, 1)
)

func dot(alive bool) string {
	if alive {
		return goodStyle.Render("●")
	}
	return dimStyle.Render("●")
}
