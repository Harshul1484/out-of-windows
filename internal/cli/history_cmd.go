package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/ui"
)

func newHistoryCmd(app *App) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "history",
		Short:   "Show what previous operations removed",
		GroupID: "tool",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			recs, err := history.Load(app.Dirs.Data, limit)
			if err != nil {
				return err
			}
			if app.JSON {
				if recs == nil {
					recs = []history.Record{}
				}
				return app.printJSON(map[string]any{"schema": "oow.history/v1", "records": recs})
			}
			app.header("History", false)
			if len(recs) == 0 {
				app.printf(" %s\n\n", ui.Muted.Render("No operations recorded yet."))
				return nil
			}
			now := time.Now()
			lastDay := ""
			for _, r := range recs {
				day := dayLabel(r.Time.Local(), now)
				if day != lastDay {
					if lastDay != "" {
						app.println()
					}
					app.printf(" %s\n", ui.Muted.Render(day))
					lastDay = day
				}
				status := ui.Muted.Render(ui.Plural(r.Removed, "item", "items"))
				if len(r.Changes) > 0 {
					status = ui.Muted.Render(ui.Plural(changedCount(r.Changes), "change", "changes"))
				}
				if r.Errors > 0 {
					status += " " + ui.Err.Render(ui.Plural(r.Errors, "error", "errors"))
				}
				if r.Cancelled {
					status += " " + ui.Warn.Render("stopped")
				}
				if r.Sandbox {
					status += " " + ui.Warn.Render("sandbox")
				}
				app.printf("   %s  %s %s reclaimed  %s\n", r.Time.Local().Format("15:04"),
					ui.PadRight(r.Command, 10), ui.PadLeft(ui.Bold.Render(ui.Bytes(r.Reclaimed)), 9), status)
			}
			app.println()
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of records (0 = all)")
	return cmd
}

// changedCount counts the changes that took effect.
func changedCount(cs []history.Change) int {
	n := 0
	for _, c := range cs {
		if c.Status == "changed" {
			n++
		}
	}
	return n
}

func dayLabel(t, now time.Time) string {
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "TODAY"
	case now.Sub(t) < 48*time.Hour && t.Day() == now.AddDate(0, 0, -1).Day():
		return "YESTERDAY"
	}
	return t.Format("Mon 2 Jan 2006")
}
