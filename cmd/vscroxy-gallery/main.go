// Command vscroxy-gallery is vscroxy's design gallery: every foundation token,
// every uikit component in every state it has, and every app screen, drawn
// from the real code with fake data.
//
//	go run ./cmd/vscroxy-gallery
package main

import (
	"log"

	"github.com/baba983/vscroxy/internal/gallery"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	g := gallery.New()
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:     "vscroxy design gallery",
			Width:     1560,
			Height:    900,
			MinWidth:  1100,
			MinHeight: 700,
			Content:   ui.View(g.View),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	g.Close()
}
