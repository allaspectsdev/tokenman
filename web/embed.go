package web

import (
	"embed"
	"io/fs"
)

//go:embed templates/* static/*
var Assets embed.FS

// StaticFS returns a sub-filesystem rooted at the static/ directory,
// suitable for serving via http.FileServer.
func StaticFS() fs.FS {
	sub, err := fs.Sub(Assets, "static")
	if err != nil {
		panic("web: embedded static filesystem not found: " + err.Error())
	}
	return sub
}
