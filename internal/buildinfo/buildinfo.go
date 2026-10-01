// Package buildinfo holds identity and version metadata for the binary.
//
// Every user-visible reference to the executable name goes through Name so the
// tool can be renamed at build time without touching the rest of the code:
//
//	go build -ldflags "-X github.com/Harshul1484/out-of-windows/internal/buildinfo.Name=oow"
package buildinfo

import (
	"fmt"
	"runtime"
)

var (
	// Name is the executable/command name shown in help and messages.
	Name = "oow"

	// DisplayName is the human-readable product name.
	DisplayName = "out-of-windows"

	// AppID names the per-user configuration and data directories. It is kept
	// separate from Name so renaming the binary does not orphan user settings.
	AppID = "oow"

	// Version, Commit and Date are injected by the release build.
	Version = "0.1.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a single-line version description.
func String() string {
	return fmt.Sprintf("%s %s (commit %s, built %s) %s/%s",
		Name, Version, Commit, Date, runtime.GOOS, runtime.GOARCH)
}
