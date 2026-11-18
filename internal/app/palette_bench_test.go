package app

import (
	"fmt"
	"testing"
)

func paletteBenchmarkApp(requests int) *app {
	a := workspaceApp()
	a.requests = make([]savedRequest, requests)
	for i := range a.requests {
		a.requests[i] = savedRequest{
			ID: fmt.Sprintf("r%d", i), Name: fmt.Sprintf("Get user %04d", i),
			Collection: fmt.Sprintf("API %d/Users", i/100), Method: "GET",
			URL: fmt.Sprintf("https://api.example.com/users/%d", i),
		}
	}
	a.requestGen++
	return a
}

var paletteBenchmarkQueries = []string{"u", "getusr", "zzzz"}

// BenchmarkPaletteRanked5000Repeated measures unchanged query frames: the
// scored and sorted asset candidates should come from the query cache.
func BenchmarkPaletteRanked5000Repeated(b *testing.B) {
	a := paletteBenchmarkApp(5000)
	for _, query := range paletteBenchmarkQueries {
		b.Run(query, func(b *testing.B) {
			a.paletteRankedResults(query, nil) // warm the query cache
			b.ResetTimer()
			for b.Loop() {
				a.paletteRankedResults(query, nil)
			}
		})
	}
}

// BenchmarkPaletteRanked5000Cold measures a new query or a changed library:
// every asset is scored once, then cached for later frames.
func BenchmarkPaletteRanked5000Cold(b *testing.B) {
	a := paletteBenchmarkApp(5000)
	for _, query := range paletteBenchmarkQueries {
		b.Run(query, func(b *testing.B) {
			for b.Loop() {
				a.paletteAssets = nil
				a.paletteRankedResults(query, nil)
			}
		})
	}
}
