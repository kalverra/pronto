// Package assets embeds pronto's brand assets: the logo and the app icon
// sizes used to build the native notification helper's .icns.
package assets

import "embed"

// Logo is the pronto logo PNG (source of truth for branding).
//
//go:embed logo.png
var Logo []byte

// AppIconSet holds the app icon PNGs keyed by size (appiconset/<size>.png),
// used to generate the native helper's .icns at install time.
//
//go:embed appiconset/*.png
var AppIconSet embed.FS

// AppIconSizes lists the PNG sizes in AppIconSet, largest last.
var AppIconSizes = []string{"16", "32", "64", "128", "256", "512", "1024"}
