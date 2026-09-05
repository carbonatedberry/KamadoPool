// Package webui embeds the built Svelte dashboard into the kamado-api
// binary so the production image is a single Go binary plus ckpool.
//
// The `dist/` subdirectory is populated at build time:
//
//   - `make api` (local) runs `make ui` first and copies ui/dist/*
//     into api/internal/webui/dist/ before `go build`.
//   - The api Dockerfile has a node builder stage that does the same
//     inside the image build.
//
// A placeholder index.html is committed so `go build` succeeds on a
// fresh checkout without anyone having run `make ui`, it just shows
// a "UI not built" notice instead of the real dashboard.
package webui

import (
	"embed"
	"io/fs"
)

// `//go:embed dist` (without `all:`) skips dot-prefixed files, which
// means the .gitignore we use to untrack build artifacts in this
// directory doesn't end up baked into the binary.
//
//go:embed dist
var distFS embed.FS

// FS returns the embedded dist directory as a sub-filesystem so
// callers can pass it directly to http.FileServerFS.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Only possible if the "dist" directory literally does not
		// exist in the embed, which is a build-time error we'd see
		// before the binary ran.
		panic("webui: embedded dist missing: " + err.Error())
	}
	return sub
}
