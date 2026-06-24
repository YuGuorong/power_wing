//go:build windows

package main

import "embed"

//go:embed app.ico
var appIcon []byte

// Keep the embed import alive for go:embed processing.
var _ embed.FS
