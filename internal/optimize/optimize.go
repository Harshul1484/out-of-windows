// Package optimize runs a short list of bounded maintenance tasks through the
// owner's own interfaces (the DNS client, the Delivery Optimization cmdlets,
// the volume optimizer). Each task states what it does, why, whether it needs
// administrator rights and what its real effect is. None of them claims to
// make Windows faster.
package optimize

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task is one maintenance task.
type Task struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	What          string `json:"what"`
	Why           string `json:"why"`
	Effect        string `json:"effect"`
	RequiresAdmin bool   `json:"requires_admin"`
}

// Task IDs.
const (
	TaskDNS     = "optimize.dns-flush"
	TaskDO      = "optimize.delivery-optimization"
	TaskReTrim  = "optimize.ssd-retrim"
	minFreeable = 1 << 20 // below 1 MB the Delivery Optimization cache is "already empty"
)

// Tasks lists every task in order.
func Tasks() []*Task {
	return []*Task{
		{
			ID:   TaskDNS,
			Name: "Flush the DNS resolver cache",
			What: "Asks the Windows DNS Client to forget the name lookups it has cached (the same as ipconfig /flushdns).",
			Why:  "Cached answers can be stale after a website, VPN or network moves to new addresses.",
			Effect: "Each name is looked up once more the next time it is used. Fixes stale-address errors; " +
				"it does not make browsing faster.",
		},
		{
			ID:   TaskDO,
			Name: "Clear the Delivery Optimization cache",
			What: "Runs Windows' own Delete-DeliveryOptimizationCache cmdlet, which removes update and Store downloads " +
				"kept for sharing with other PCs. Pinned files are kept.",
			Why:           "The cache only serves downloads that already finished; Windows refills it as needed.",
			Effect:        "Frees the space the cache used. Other PCs on your network can no longer download those updates from this one.",
			RequiresAdmin: true,
		},
		{
			ID:   TaskReTrim,
			Name: "Retrim SSD volumes",
			What: "Runs Optimize-Volume -ReTrim on fixed SSD volumes, telling each drive which blocks are free.",
			Why:  "Windows already retrims weekly (Optimize Drives) and on every delete; a retrim catches up if that was missed.",
			Effect: "Can restore write speed on a drive whose free space was not trimmed. On most systems there is no " +
				"measurable change. Hard disks are never touched (no defragmentation).",
			RequiresAdmin: true,
		},
	}
}

// Volume is a fixed volume eligible for retrim.
type Volume struct {
	Root       string `json:"root"`
	FileSystem string `json:"file_system"`
}

// DOCache describes the Delivery Optimization cache.
type DOCache struct {
	// Available is false when this Windows has no Delivery Optimization
	// cmdlets (Server Core, old builds).
	Available bool `json:"available"`
	// Bytes is the cache size, or -1 when it could not be measured.
	Bytes int64  `json:"bytes"`
	Path  string `json:"path"`
}

// Runner performs the tasks. Real and simulated implementations exist; the
// methods that change something are only called for confirmed tasks.
type Runner interface {
	FlushDNS(ctx context.Context) error
	DeliveryOptimization(ctx context.Context, elevated bool) DOCache
	ClearDeliveryOptimization(ctx context.Context) error
	SSDVolumes(ctx context.Context) ([]Volume, error)
	ReTrim(ctx context.Context, v Volume) error
}

// Status of a planned task.
type Status string

// Statuses.
const (
	Ready         Status = "ready"
	NeedsAdmin    Status = "needs-admin"
	NotApplicable Status = "not-applicable" // nothing to do (e.g. no SSDs, empty cache)
	Unavailable   Status = "unavailable"    // this Windows cannot run it
)

// Item is one task in the plan.
type Item struct {
	Task   *Task  `json:"task"`
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Selected is whether it runs (by default every ready task).
	Selected bool `json:"selected"`
	// BytesBefore is the Delivery Optimization cache size (-1 unknown).
	BytesBefore int64    `json:"bytes_before,omitempty"`
	Volumes     []Volume `json:"volumes,omitempty"`
}

// ReasonNeedsAdmin explains admin-only tasks in a non-elevated process.
const ReasonNeedsAdmin = "requires administrator: run oow from an elevated terminal"

// Plan describes what each task would do now. It changes nothing.
func Plan(ctx context.Context, sys Runner, elevated bool, tasks []*Task) []Item {
	var out []Item
	for _, t := range tasks {
		it := Item{Task: t, Status: Ready}
		switch t.ID {
		case TaskDO:
			c := sys.DeliveryOptimization(ctx, elevated)
			it.BytesBefore = c.Bytes
			switch {
			case !c.Available:
				it.Status, it.Reason = Unavailable, "this Windows has no Delivery Optimization cmdlets"
			case !elevated:
				it.Status, it.Reason = NeedsAdmin, ReasonNeedsAdmin
			case c.Bytes >= 0 && c.Bytes < minFreeable:
				it.Status, it.Reason = NotApplicable, "the cache is already empty"
			}
		case TaskReTrim:
			vols, err := sys.SSDVolumes(ctx)
			it.Volumes = vols
			switch {
			case err != nil:
				it.Status, it.Reason = Unavailable, "could not list SSD volumes: "+err.Error()
			case len(vols) == 0:
				it.Status, it.Reason = NotApplicable, "no fixed SSD volumes with TRIM support"
			case !elevated:
				it.Status, it.Reason = NeedsAdmin, ReasonNeedsAdmin
			}
		default:
			if t.RequiresAdmin && !elevated {
				it.Status, it.Reason = NeedsAdmin, ReasonNeedsAdmin
			}
		}
		it.Selected = it.Status == Ready
		out = append(out, it)
	}
	return out
}

// Result is the outcome of one task.
type Result struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"` // done, failed, partial, cancelled
	Message string `json:"message"`
	// BytesBefore/BytesAfter: Delivery Optimization cache size around the
	// run (-1 unknown); Freed is the measured difference.
	BytesBefore int64          `json:"bytes_before,omitempty"`
	BytesAfter  int64          `json:"bytes_after,omitempty"`
	Freed       int64          `json:"freed_bytes,omitempty"`
	Volumes     []VolumeResult `json:"volumes,omitempty"`
	DurationMS  int64          `json:"duration_ms"`
}

// VolumeResult is the retrim outcome of one volume.
type VolumeResult struct {
	Root  string `json:"root"`
	Error string `json:"error,omitempty"`
}

// Result statuses.
const (
	Done      = "done"
	Failed    = "failed"
	Partial   = "partial"
	Cancelled = "cancelled"
)

// Run runs the selected ready items in order. Cancellation stops before the
// next task (and before the next volume of a retrim).
func Run(ctx context.Context, sys Runner, items []Item) []Result {
	var out []Result
	for _, it := range items {
		if !it.Selected || it.Status != Ready {
			continue
		}
		r := Result{ID: it.Task.ID, Name: it.Task.Name}
		if ctx.Err() != nil {
			r.Status, r.Message = Cancelled, "not started: cancelled"
			out = append(out, r)
			continue
		}
		start := time.Now()
		switch it.Task.ID {
		case TaskDNS:
			if err := sys.FlushDNS(ctx); err != nil {
				r.Status, r.Message = Failed, err.Error()
			} else {
				r.Status, r.Message = Done, "the DNS resolver cache was flushed"
			}
		case TaskDO:
			r.BytesBefore = it.BytesBefore
			err := sys.ClearDeliveryOptimization(ctx)
			after := sys.DeliveryOptimization(context.WithoutCancel(ctx), true)
			r.BytesAfter = after.Bytes
			if r.BytesBefore >= 0 && r.BytesAfter >= 0 && r.BytesBefore > r.BytesAfter {
				r.Freed = r.BytesBefore - r.BytesAfter
			}
			switch {
			case err != nil:
				r.Status, r.Message = Failed, err.Error()
			case r.BytesAfter < 0:
				r.Status, r.Message = Done, "the cache was cleared (its size could not be measured)"
			default:
				r.Status, r.Message = Done, fmt.Sprintf("the cache was cleared (%s freed)", sizeString(r.Freed))
			}
		case TaskReTrim:
			var failed int
			for _, v := range it.Volumes {
				if ctx.Err() != nil {
					break
				}
				vr := VolumeResult{Root: v.Root}
				if err := sys.ReTrim(ctx, v); err != nil {
					vr.Error = err.Error()
					failed++
				}
				r.Volumes = append(r.Volumes, vr)
			}
			var parts []string
			for _, v := range r.Volumes {
				if v.Error == "" {
					parts = append(parts, "retrimmed "+v.Root)
				} else {
					parts = append(parts, v.Root+" failed: "+v.Error)
				}
			}
			if n := len(it.Volumes) - len(r.Volumes); n > 0 {
				parts = append(parts, fmt.Sprintf("%d not started (cancelled)", n))
			}
			r.Message = strings.Join(parts, "; ")
			switch {
			case len(r.Volumes) == 0:
				r.Status = Cancelled
			case failed == len(it.Volumes):
				r.Status = Failed
			case failed > 0 || len(r.Volumes) < len(it.Volumes):
				r.Status = Partial
			default:
				r.Status = Done
			}
		default:
			r.Status, r.Message = Failed, "unknown task"
		}
		r.DurationMS = time.Since(start).Milliseconds()
		out = append(out, r)
	}
	return out
}

// Select narrows tasks to the IDs or dotted prefixes in filters.
func Select(filters []string) ([]*Task, error) {
	all := Tasks()
	if len(filters) == 0 {
		return all, nil
	}
	var out []*Task
	for _, t := range all {
		for _, f := range filters {
			f = strings.ToLower(strings.TrimSpace(f))
			short := strings.TrimPrefix(t.ID, "optimize.")
			if f == t.ID || f == short || strings.HasPrefix(t.ID, f+".") || f == "optimize" {
				out = append(out, t)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no task matches " + strings.Join(filters, ", "))
	}
	return out, nil
}

func sizeString(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
