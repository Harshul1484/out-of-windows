package cli

import (
	"github.com/Harshul1484/out-of-windows/internal/cleanup"
)

// JSON output types. Field names are a stable interface documented in
// docs/JSON.md: fields may be added, never renamed or removed, within a
// schema version.

type cleanJSON struct {
	Schema   string           `json:"schema"`
	DryRun   bool             `json:"dry_run"`
	Sandbox  bool             `json:"sandbox"`
	Elevated bool             `json:"elevated"`
	Rules    []cleanRuleJSON  `json:"rules"`
	Summary  cleanSummaryJSON `json:"summary"`
}

type cleanRuleJSON struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Category    string                `json:"category"`
	Status      string                `json:"status"`
	Reason      string                `json:"reason,omitempty"`
	Selected    bool                  `json:"selected"`
	Roots       []string              `json:"roots"`
	Files       int                   `json:"files"`
	Bytes       int64                 `json:"bytes"`
	KeptRecent  int                   `json:"kept_recent"`
	Skipped     int                   `json:"skipped"`
	SkipReasons []cleanup.ReasonCount `json:"skip_reasons"`
	Items       []itemJSON            `json:"items,omitempty"`
	Result      *cleanResultJSON      `json:"result,omitempty"`
}

type itemJSON struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type cleanResultJSON struct {
	Removed     int                   `json:"removed"`
	DirsRemoved int                   `json:"dirs_removed"`
	Reclaimed   int64                 `json:"reclaimed_bytes"`
	Skipped     int                   `json:"skipped"`
	SkipReasons []cleanup.ReasonCount `json:"skip_reasons"`
	Errors      int                   `json:"errors"`
}

type cleanSummaryJSON struct {
	ReclaimableFiles int   `json:"reclaimable_files"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
	SelectedFiles    int   `json:"selected_files"`
	SelectedBytes    int64 `json:"selected_bytes"`
	Executed         bool  `json:"executed"`
	Removed          int   `json:"removed"`
	ReclaimedBytes   int64 `json:"reclaimed_bytes"`
	FreedOnDiskBytes int64 `json:"freed_on_disk_bytes"`
	Skipped          int   `json:"skipped"`
	Errors           int   `json:"errors"`
	Cancelled        bool  `json:"cancelled"`
	ScanMS           int64 `json:"scan_ms"`
	CleanMS          int64 `json:"clean_ms"`
}

func cleanReport(app *App, res *cleanup.ScanResult, selected map[string]bool, out *cleanup.Outcome, dryRun, details bool) cleanJSON {
	r := cleanJSON{
		Schema:   "oow.clean/v1",
		DryRun:   dryRun,
		Sandbox:  app.Sandbox != "",
		Elevated: app.Elevated,
		Summary: cleanSummaryJSON{
			ReclaimableFiles: res.Files(),
			ReclaimableBytes: res.Bytes(),
			ScanMS:           res.Duration.Milliseconds(),
		},
	}
	outcomes := map[string]*cleanup.RuleOutcome{}
	if out != nil {
		for _, ro := range out.Rules {
			outcomes[ro.Rule.ID] = ro
		}
		r.Summary.Executed = true
		r.Summary.Removed = out.Removed
		r.Summary.ReclaimedBytes = out.Reclaimed
		r.Summary.FreedOnDiskBytes = out.FreedOnDisk()
		r.Summary.Skipped = out.Skipped
		r.Summary.Errors = out.Errors
		r.Summary.Cancelled = out.Cancelled
		r.Summary.CleanMS = out.Duration.Milliseconds()
	}
	for _, rs := range res.Rules {
		sel := selected[rs.Rule.ID] && rs.Status == cleanup.StatusReady
		j := cleanRuleJSON{
			ID:          rs.Rule.ID,
			Name:        rs.Rule.Name,
			Category:    string(rs.Rule.Category),
			Status:      string(rs.Status),
			Reason:      rs.Reason,
			Selected:    sel,
			Roots:       nonNil(rs.Roots),
			Files:       rs.ItemCount(),
			Bytes:       rs.Bytes,
			KeptRecent:  rs.KeptRecent,
			Skipped:     rs.Skipped.Total(),
			SkipReasons: nonNilReasons(rs.Skipped.Reasons()),
		}
		if sel {
			r.Summary.SelectedFiles += rs.ItemCount()
			r.Summary.SelectedBytes += rs.Bytes
		}
		if details {
			for _, it := range rs.Files {
				j.Items = append(j.Items, itemJSON{Path: it.Path, Size: it.Fingerprint.Size})
			}
		}
		if ro := outcomes[rs.Rule.ID]; ro != nil {
			j.Result = &cleanResultJSON{
				Removed:     ro.Removed,
				DirsRemoved: ro.DirsRemoved,
				Reclaimed:   ro.Reclaimed,
				Skipped:     ro.Skipped.Total(),
				SkipReasons: nonNilReasons(ro.Skipped.Reasons()),
				Errors:      ro.Errors,
			}
		}
		r.Rules = append(r.Rules, j)
	}
	return r
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilReasons(s []cleanup.ReasonCount) []cleanup.ReasonCount {
	if s == nil {
		return []cleanup.ReasonCount{}
	}
	return s
}
