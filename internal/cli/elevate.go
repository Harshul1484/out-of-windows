package cli

import (
	"errors"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/elevation"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

// relaunchElevated runs oow with args in a new elevated window (UAC prompt)
// and waits for it.
func relaunchElevated(args []string) (uint32, error) { return elevation.Relaunch(args) }

// reportElevated tells the user how an elevated window ended.
func reportElevated(app *App, code uint32, err error) {
	switch {
	case errors.Is(err, elevation.ErrDeclined):
		app.printf(" %s\n\n", ui.Muted.Render("Administrator permission was not granted. Nothing else was changed."))
	case err != nil:
		app.printf(" %s could not start an elevated window: %v\n\n", ui.Err.Render(ui.SymErr), err)
	default:
		app.printf(" %s The elevated window finished (exit code %d). See `%s history` for what it changed.\n\n",
			ui.OK.Render(ui.SymOK), code, buildinfo.Name)
	}
}
