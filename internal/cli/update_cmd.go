package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/buildinfo"
	"github.com/Harshul1484/out-of-windows/internal/filesystem"
	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/install"
	"github.com/Harshul1484/out-of-windows/internal/selfupdate"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

type updateOptions struct {
	check  bool
	dryRun bool
	yes    bool
}

func newUpdateCmd(app *App) *cobra.Command {
	var o updateOptions
	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Update " + buildinfo.Name + " to the latest release",
		GroupID: "tool",
		Long: "Check GitHub for the latest release, download the build for this computer, verify its\n" +
			"SHA-256 against the release's SHA256SUMS file, and replace this executable. Nothing is\n" +
			"installed if any check fails. The previous version is kept next to the new one as\n" +
			buildinfo.Name + ".exe.old and removed the next time " + buildinfo.Name + " starts.\n\n" +
			"Installs managed by winget, Scoop or Chocolatey are updated with that package manager.\n" +
			"For a private repository, set GITHUB_TOKEN or GH_TOKEN to a token that can read it.",
		Example: "  " + buildinfo.Name + " update --check\n" +
			"  " + buildinfo.Name + " update --dry-run\n" +
			"  " + buildinfo.Name + " update --yes --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd.Context(), app, o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.check, "check", false, "only report whether a newer release exists")
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "show what would be downloaded and replaced, without changing anything")
	f.BoolVarP(&o.yes, "yes", "y", false, "do not ask for confirmation (required for non-interactive use)")
	return cmd
}

// updateJSON is the oow.update/v1 document.
type updateJSON struct {
	Schema          string             `json:"schema"`
	CheckOnly       bool               `json:"check_only"`
	DryRun          bool               `json:"dry_run"`
	Sandbox         bool               `json:"sandbox"`
	Repo            string             `json:"repo"`
	CurrentVersion  string             `json:"current_version"`
	DevBuild        bool               `json:"dev_build"`
	LatestVersion   string             `json:"latest_version"`
	UpdateAvailable bool               `json:"update_available"`
	ManagedBy       *managedJSON       `json:"managed_by"`
	Release         *updateReleaseJSON `json:"release,omitempty"`
	Asset           *updateAssetJSON   `json:"asset,omitempty"`
	Executable      string             `json:"executable"`
	Backup          string             `json:"backup,omitempty"`
	Updated         bool               `json:"updated"`
	Message         string             `json:"message"`
	Error           string             `json:"error,omitempty"`
}

type managedJSON struct {
	Manager       string `json:"manager"`
	Package       string `json:"package"`
	UpdateCommand string `json:"update_command"`
	RemoveCommand string `json:"remove_command"`
}

type updateReleaseJSON struct {
	Tag         string    `json:"tag"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at"`
}

type updateAssetJSON struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func managedDoc(m install.Managed) *managedJSON {
	if m.Manager == install.None {
		return nil
	}
	return &managedJSON{Manager: string(m.Manager), Package: m.Package,
		UpdateCommand: m.UpdateCommand(), RemoveCommand: m.RemoveCommand()}
}

func runUpdate(ctx context.Context, app *App, o updateOptions) error {
	if app.ForceDryRun {
		o.dryRun = true
	}
	doc := updateJSON{
		Schema: "oow.update/v1", CheckOnly: o.check, DryRun: o.dryRun, Sandbox: app.Sandbox != "",
		Repo: buildinfo.Repo, CurrentVersion: buildinfo.Version, DevBuild: selfupdate.IsDevBuild(buildinfo.Version),
	}
	// done finishes the command: exactly one JSON document, or the text
	// message (errors are printed by Run with their exit code).
	done := func(code int, msg string) error {
		doc.Message = msg
		if code != ExitOK {
			doc.Error = msg
		}
		if !app.JSON {
			if code != ExitOK {
				return withCode(code, "%s", msg)
			}
			app.printf(" %s\n\n", msg)
			return nil
		}
		if err := app.printJSON(doc); err != nil {
			return err
		}
		if code != ExitOK {
			return alreadyReported(code, "%s", msg)
		}
		return nil
	}

	self, err := app.self()
	if err != nil {
		return withCode(ExitError, "cannot locate this executable: %v", err)
	}
	doc.Executable = self.Exe
	managed := managedBy(self)
	doc.ManagedBy = managedDoc(managed)
	if !app.JSON {
		app.header("Update", o.dryRun && !o.check)
	}

	// Refusals that need no network: the package manager owns this copy, or
	// a development build cannot order itself against releases.
	if !o.check && managed.Manager != install.None {
		return done(ExitError, fmt.Sprintf("%s was installed with %s; update it with: %s",
			buildinfo.Name, managed.Label(), managed.UpdateCommand()))
	}
	if !o.check && doc.DevBuild {
		return done(ExitError, fmt.Sprintf("this is a development build (%s), which cannot update itself; "+
			"install a release with the installer instead (see the README)", buildinfo.Version))
	}

	src := app.updateSource()
	spin := ui.StartSpinner(app.Err, app.tty(), func() string { return "Checking " + buildinfo.Repo + " for releases" })
	rel, err := src.Latest(ctx)
	spin.Stop()
	if ctx.Err() != nil {
		return errCancelled
	}
	if err != nil {
		return done(ExitError, err.Error())
	}
	latest, _ := rel.Version()
	doc.LatestVersion = latest.String()
	doc.Release = &updateReleaseJSON{Tag: rel.Tag, URL: rel.URL, PublishedAt: rel.PublishedAt}

	cmp := 0
	if !doc.DevBuild {
		cur, _ := selfupdate.ParseVersion(buildinfo.Version)
		cmp = cur.Compare(latest)
		doc.UpdateAvailable = cmp < 0
	}
	if !app.JSON {
		printVersions(app, doc, self)
	}

	if o.check {
		switch {
		case doc.DevBuild:
			return done(ExitOK, fmt.Sprintf("This is a development build; the latest release is %s.", latest))
		case !doc.UpdateAvailable:
			return done(ExitOK, upToDate(cmp, latest))
		case managed.Manager != install.None:
			return done(ExitOK, fmt.Sprintf("%s is available. Update with: %s", latest, managed.UpdateCommand()))
		}
		return done(ExitOK, fmt.Sprintf("%s is available. Run `%s update` to install it.", latest, buildinfo.Name))
	}
	if !doc.UpdateAvailable {
		return done(ExitOK, upToDate(cmp, latest))
	}

	if _, err := statFile(self.Exe); err != nil {
		return done(ExitError, fmt.Sprintf("no executable to replace at %s (%v)", self.Exe, err))
	}
	plan, err := src.PlanFor(ctx, rel, buildinfo.Name, runtime.GOARCH)
	if ctx.Err() != nil {
		return errCancelled
	}
	if err != nil {
		return done(ExitError, err.Error())
	}
	doc.Asset = &updateAssetJSON{Name: plan.Asset.Name, Size: plan.Asset.Size, SHA256: plan.SHA256}
	doc.Backup = selfupdate.OldPath(self.Exe)
	if !app.JSON {
		app.printf("   %s %s %s\n", ui.Muted.Render(ui.PadRight("Download", 11)), plan.Asset.Name, ui.Muted.Render(ui.Bytes(plan.Asset.Size)))
		app.printf("   %s %s %s\n", ui.Muted.Render(ui.PadRight("SHA-256", 11)), plan.SHA256,
			ui.Muted.Render("(from "+selfupdate.ChecksumsName+"; checked before anything is replaced)"))
		app.printf("   %s %s %s\n\n", ui.Muted.Render(ui.PadRight("Previous", 11)), doc.Backup,
			ui.Muted.Render("(kept until the next start)"))
	}
	if o.dryRun {
		return done(ExitOK, "Dry run: nothing was downloaded or changed.")
	}

	switch {
	case app.interactive() && !o.yes:
		ok, err := confirmCtx(ctx, app, fmt.Sprintf(" Update %s %s → %s?", buildinfo.Name, buildinfo.Version, latest))
		if err != nil {
			return err
		}
		if !ok {
			return done(ExitOK, "Nothing was changed.")
		}
	case !o.yes:
		return done(ExitNeedsConfirm, "refusing to update without confirmation: pass --yes, or use --dry-run to preview")
	}

	start := time.Now()
	spin = ui.StartSpinner(app.Err, app.tty(), func() string { return "Downloading and verifying " + plan.Asset.Name })
	staged, err := selfupdate.Stage(ctx, src, plan, self.Exe)
	spin.Stop()
	if err != nil {
		if ctx.Err() != nil {
			return errCancelled
		}
		return done(ExitError, "update stopped, nothing was replaced: "+err.Error())
	}
	if err := selfupdate.Replace(self.Exe, staged); err != nil {
		return done(ExitError, err.Error())
	}
	doc.Updated = true
	app.record(history.Record{Time: time.Now(), Command: "update", Sandbox: app.Sandbox != "",
		DurationMS: time.Since(start).Milliseconds(),
		Targets:    []history.TargetStat{{ID: "self", Name: fmt.Sprintf("%s %s -> %s", buildinfo.Name, buildinfo.Version, latest)}}})
	if !app.JSON {
		app.printf(" %s Updated %s to %s.\n", ui.OK.Render(ui.SymOK), buildinfo.Name, latest)
	}
	return done(ExitOK, fmt.Sprintf("The previous version stays as %s until %s next starts.",
		filepath.Base(doc.Backup), buildinfo.Name))
}

func upToDate(cmp int, latest selfupdate.Version) string {
	if cmp > 0 {
		return fmt.Sprintf("This version is newer than the latest release (%s). Nothing to do.", latest)
	}
	return fmt.Sprintf("%s %s is up to date.", buildinfo.Name, buildinfo.Version)
}

func printVersions(app *App, doc updateJSON, self selfInfo) {
	cur := buildinfo.Version
	if doc.DevBuild {
		cur += " " + ui.Muted.Render("(development build)")
	}
	app.printf("   %s %s  %s\n", ui.Muted.Render(ui.PadRight("Installed", 11)), cur, ui.Muted.Render(self.Exe))
	latest := doc.LatestVersion
	if doc.Release != nil && !doc.Release.PublishedAt.IsZero() {
		latest += "  " + ui.Muted.Render("released "+doc.Release.PublishedAt.Local().Format("2006-01-02"))
	}
	app.printf("   %s %s\n", ui.Muted.Render(ui.PadRight("Latest", 11)), latest)
	if doc.ManagedBy != nil {
		app.printf("   %s %s\n", ui.Muted.Render(ui.PadRight("Managed by", 11)), doc.ManagedBy.Manager)
	}
	app.println()
}

// statFile reports whether path is an existing plain file.
func statFile(path string) (int64, error) {
	e, err := filesystem.Lstat(path)
	if err != nil {
		return 0, err
	}
	if e.IsDir() || e.Reparse {
		return 0, errors.New("not a plain file")
	}
	return e.Size(), nil
}
