package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var files embed.FS

func Handler() http.Handler {
	content, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(content))
}
