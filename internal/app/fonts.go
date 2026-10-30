package app

import (
	"embed"
	"fmt"
	"log"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Keep the upstream licenses in the executable alongside the unmodified fonts.
// Provenance and checksums live in fonts/README.md.
//
//go:embed fonts/*.ttf fonts/*-OFL.txt
var appFontFiles embed.FS

// registerAppFonts registers every bundled weight once, before appTheme uses
// "Inter" or "JetBrains Mono". Keep platform CJK fallbacks in the theme's family
// list; these fonts intentionally do not bundle Chinese glyphs.
// Missing embedded assets are a build invariant violation. OS registration
// failures are logged so native fallback fonts can keep the app usable.
var registerAppFonts = sync.OnceFunc(func() {
	for _, font := range []struct {
		file   string
		family string
	}{
		{"Inter-Regular.ttf", "Inter"},
		{"Inter-Medium.ttf", "Inter"},
		{"Inter-SemiBold.ttf", "Inter"},
		{"JetBrainsMono-Regular.ttf", "JetBrains Mono"},
		{"JetBrainsMono-Bold.ttf", "JetBrains Mono"},
	} {
		data, err := appFontFiles.ReadFile("fonts/" + font.file)
		if err != nil {
			panic(fmt.Errorf("read embedded font %s: %w", font.file, err))
		}
		if err := ui.RegisterFont(data, font.family); err != nil {
			log.Printf("register embedded font %s: %v; using available fallback fonts", font.file, err)
		}
	}
})
