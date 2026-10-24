// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web holds the user interface. This branch builds with Go 1.12 (the
// last Go for macOS 10.10), which has no embed package: macos/legacyweb.py
// turns the files of this folder into assets_gen.go during the build, with
// the script and styles adapted to the WebKit of old macOS versions.
package web

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"time"
)

// Files maps file names ("app.js", "img/mic.png") to their contents.
var Files = map[string][]byte{}

var modTime = time.Now()

// ReadFile returns a file of the user interface.
func ReadFile(name string) ([]byte, error) {
	if b, ok := Files[name]; ok {
		return b, nil
	}
	return nil, os.ErrNotExist
}

// Handler serves the files; the request path is the file name.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		b, ok := Files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, name, modTime, bytes.NewReader(b))
	})
}
