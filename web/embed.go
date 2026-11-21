// Package web exposes the compiled front end (web/dist) for embedding.
package web

import "embed"

// Dist holds the built SPA. Serve it with fs.Sub(Dist, "dist").
// dist/.gitkeep keeps this compiling before `npm run build`.
//
//go:embed all:dist
var Dist embed.FS
