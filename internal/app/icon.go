package app

import _ "embed"

// iconPNG is the app's icon in the binary. A run that is not a bundle — go
// run, mygo dev — has no .icns or executable resource to read it from, and
// the Dock would show the generic executable instead. resources/icon.png is
// the copy mygo build packages; assets/icon.svg is their source, and
// assets/README.md says how to make both from it.
//
//go:embed icon.png
var iconPNG []byte
