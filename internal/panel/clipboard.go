package panel

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
)

// copyToClipboard mirrors tui-panel/server-panel.py's copy_to_clipboard():
// the same native-tool candidates per OS, tried in the same order. Returns
// true only when one of them actually ran successfully.
func copyToClipboard(text string) bool {
	var candidates [][]string
	switch runtime.GOOS {
	case "windows":
		candidates = [][]string{{"clip"}}
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	default:
		candidates = [][]string{
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
			{"wl-copy"},
		}
	}
	for _, args := range candidates {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}

// copyToClipboardOSC52 is the fallback for a headless remote server (no
// xclip/xsel/wl-copy target to talk to, e.g. reached over SSH with no X/
// Wayland display) -- same reasoning as server-panel.py's fallback to
// Textual's App.copy_to_clipboard. OSC 52 is a terminal escape sequence
// most modern terminal emulators (Windows Terminal, iTerm2, kitty,
// Alacritty, WezTerm, ...) intercept and copy straight into the *local*
// client's clipboard, even across SSH. Written directly to the real
// stdout rather than through Bubble Tea's renderer -- harmless since OSC
// sequences produce no visible glyphs, and this needs to reach the actual
// terminal, not the TUI's internal frame buffer.
func copyToClipboardOSC52(text string) {
	_, _ = osc52.New(text).WriteTo(os.Stdout)
}
