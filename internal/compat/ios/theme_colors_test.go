package ios

import (
	"testing"

	"github.com/owpengram/owpengram-td/tg"
)

func TestProjectThemesMakesOnlyIOSAccentColorsOpaque(t *testing.T) {
	settings := tg.ThemeSettings{
		BaseTheme:   &tg.BaseThemeClassic{},
		AccentColor: 0x29b071,
	}
	settings.SetOutboxAccentColor(0x4cb064)
	settings.SetMessageColors([]int{0xd4f1ff, 0xb9e4ff})
	wallpaper := &tg.WallPaperNoFile{}
	settings.SetWallpaper(wallpaper)
	theme := tg.Theme{ID: 1, Slug: "green", Title: "Green"}
	theme.SetSettings([]tg.ThemeSettings{settings})

	projected := ProjectThemes([]tg.Theme{theme})
	if len(projected) != 1 {
		t.Fatalf("ProjectThemes length = %d, want 1", len(projected))
	}
	got, ok := projected[0].GetSettings()
	if !ok || len(got) != 1 {
		t.Fatalf("projected settings = %#v ok=%v, want one setting", got, ok)
	}
	if color := uint32(int32(got[0].AccentColor)); color != 0xff29b071 {
		t.Fatalf("accent color = %#08x, want opaque ARGB 0xff29b071", color)
	}
	if color, ok := got[0].GetOutboxAccentColor(); !ok || uint32(int32(color)) != 0xff4cb064 {
		t.Fatalf("outbox accent = %#08x ok=%v, want opaque ARGB 0xff4cb064", uint32(int32(color)), ok)
	}
	colors, ok := got[0].GetMessageColors()
	if !ok || len(colors) != 2 || colors[0] != 0xd4f1ff || colors[1] != 0xb9e4ff {
		t.Fatalf("message colors = %#v ok=%v, want unchanged RGB24 values", colors, ok)
	}
	if got[0].Wallpaper != wallpaper {
		t.Fatal("wallpaper changed during accent-only projection")
	}

	sourceSettings, _ := theme.GetSettings()
	if sourceSettings[0].AccentColor != 0x29b071 {
		t.Fatalf("source accent mutated to %#x", sourceSettings[0].AccentColor)
	}
	sourceOutbox, _ := sourceSettings[0].GetOutboxAccentColor()
	if sourceOutbox != 0x4cb064 {
		t.Fatalf("source outbox accent mutated to %#x", sourceOutbox)
	}
	colors[0] = 1
	sourceColors, _ := sourceSettings[0].GetMessageColors()
	if sourceColors[0] != 0xd4f1ff {
		t.Fatalf("source message colors mutated through projection: %#v", sourceColors)
	}
}

func TestProjectThemesPreservesExistingAlphaAndAbsentOutbox(t *testing.T) {
	argb := uint32(0x80445566)
	settings := tg.ThemeSettings{
		BaseTheme:   &tg.BaseThemeTinted{},
		AccentColor: int(int32(argb)),
	}
	theme := tg.Theme{ID: 2}
	theme.SetSettings([]tg.ThemeSettings{settings})

	projected := ProjectThemes([]tg.Theme{theme})
	got, _ := projected[0].GetSettings()
	if color := uint32(int32(got[0].AccentColor)); color != argb {
		t.Fatalf("existing ARGB color = %#08x, want %#08x", color, argb)
	}
	if _, ok := got[0].GetOutboxAccentColor(); ok {
		t.Fatal("absent outbox accent became present")
	}
}
