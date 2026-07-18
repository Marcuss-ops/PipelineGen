package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS returns the embedded dist filesystem with the "dist" prefix
// stripped so that index.html is at the root.
func DistFS() fs.FS {
	fsys, err := fs.Sub(distFS, "dist")
	if err != nil {
		// The embed pattern guarantees dist exists, so this should never happen.
		panic(err)
	}
	return fsys
}
