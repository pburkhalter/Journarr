// Package web embeds the built frontend. frontend/ builds into dist/ here and
// the build is committed, so `go build` works without Node; CI fails when the
// committed build no longer matches the source.
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
