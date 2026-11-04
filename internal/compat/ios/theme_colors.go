package ios

import "github.com/owpengram/owpengram-td/tg"

// ProjectThemes converts theme accent colors to the ARGB representation used by
// Telegram-iOS. Official chat-theme seed colors are RGB24 values, while iOS
// passes accent_color and outbox_accent_color to UIColor(argb:); a zero high
// byte would therefore make every accent-tinted control fully transparent.
//
// The projection is copy-on-write so the Android/TDesktop catalog remains
// byte-for-byte unchanged. message_colors and wallpaper colors intentionally
// stay RGB24, as required by the TL schema and all audited clients.
func ProjectThemes(themes []tg.Theme) []tg.Theme {
	if len(themes) == 0 {
		return nil
	}
	out := make([]tg.Theme, len(themes))
	for i := range themes {
		out[i] = themes[i]
		settings, ok := themes[i].GetSettings()
		if !ok {
			continue
		}
		projected := make([]tg.ThemeSettings, len(settings))
		for j := range settings {
			projected[j] = settings[j]
			projected[j].AccentColor = opaqueARGB(settings[j].AccentColor)
			if color, ok := settings[j].GetOutboxAccentColor(); ok {
				projected[j].SetOutboxAccentColor(opaqueARGB(color))
			}
			if colors, ok := settings[j].GetMessageColors(); ok {
				projected[j].SetMessageColors(append([]int(nil), colors...))
			}
		}
		out[i].SetSettings(projected)
	}
	return out
}

func opaqueARGB(color int) int {
	value := uint32(int32(color))
	if value>>24 == 0 {
		value |= 0xff000000
	}
	return int(int32(value))
}
