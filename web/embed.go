// SPDX-License-Identifier: AGPL-3.0-or-later
// Package web holds the embedded user interface.
package web

import "embed"

//go:embed index.html app.js style.css img/*.png
var FS embed.FS
