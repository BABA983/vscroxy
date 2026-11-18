package app

import "testing"

// BenchmarkActionsInFrame measures the command work one UI frame asks for:
// the palette, the native menu state, buttons and the shortcut dispatcher all
// need the active commands. Each iteration is one frame; a.builds is the
// app's existing view-pass counter and is what invalidates the frame's cache.
func BenchmarkActionsInFrame(b *testing.B) {
	a := workspaceApp()
	all := []string{"request.send", "request.save", "capture.stop", "capture.start", "app.palette"}
	for b.Loop() {
		a.builds++
		_ = a.pickActions(nil, all...)
		for _, x := range a.actions(nil) {
			if x.enabled && x.shortcut.key != 0 {
				_ = x.shortcut.accelerator()
			}
		}
		_ = a.actions(nil)
	}
}
