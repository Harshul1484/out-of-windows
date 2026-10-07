// Command oow is a Windows-native system maintenance CLI.
package main

import (
	"os"

	"github.com/Harshul1484/out-of-windows/internal/cli"
)

// The committed rsrc_windows_*.syso files embed the version information and
// application manifest from winres/ into oow.exe, so a plain `go build` gets
// them. They carry the development version (buildinfo.Version); the release
// workflow regenerates them with the release version before building, and CI
// checks that the committed files match this command's output.
//
//go:generate go tool -C ../../tools go-winres make --in ../cmd/oow/winres/winres.json --out ../cmd/oow/rsrc --arch amd64,arm64 --file-version 0.1.0-dev --product-version 0.1.0-dev

func main() {
	os.Exit(cli.Main())
}
