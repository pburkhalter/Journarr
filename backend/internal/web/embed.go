// Package web embeds the built frontend. frontend/ builds into dist/ here;
// before the first build only the placeholder .gitignore exists and the API
// serves a plain notice instead.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
