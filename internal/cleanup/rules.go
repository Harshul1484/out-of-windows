package cleanup

import "time"

const (
	hour = time.Hour
	day  = 24 * time.Hour
)

// registry holds every built-in rule. Rule files register themselves from
// init functions so each category lives in its own file.
var registry []*Rule

func register(rules ...*Rule) { registry = append(registry, rules...) }

// BuiltinRules returns all built-in rules in display order.
func BuiltinRules() []*Rule {
	out := make([]*Rule, 0, len(registry))
	for _, c := range CategoryOrder {
		for _, r := range registry {
			if r.Category == c {
				out = append(out, r)
			}
		}
	}
	return out
}

// FindRule returns the built-in rule with the given ID.
func FindRule(id string) *Rule {
	for _, r := range registry {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func init() {
	register(
		&Rule{
			ID:       "temp.user",
			Name:     "User temporary files",
			Category: CategoryTemp,
			Roots:    []string{`{LocalAppData}\Temp`},
			MinAge:   24 * hour,
			What:     "Files in your Temp folder that were created and last modified more than 24 hours ago.",
			WhySafe: "Programs put short-lived working files here. Anything older than a day that is not " +
				"open by a program is leftover from installers, updates and crashed apps. Files in use are skipped.",
			Impact:          "None expected. Programs recreate temporary files when they need them.",
			DefaultSelected: true,
		},
		&Rule{
			ID:       "temp.windows",
			Name:     "Windows temporary files",
			Category: CategoryTemp,
			Roots:    []string{`{WindowsTemp}`},
			MinAge:   24 * hour,
			What:     "Files in the system Temp folder (Windows\\Temp) older than 24 hours.",
			WhySafe: "Windows services and installers use this folder for working files; Windows Disk Cleanup " +
				"removes the same files. Files in use are skipped.",
			Impact:          "None expected.",
			RequiresAdmin:   true,
			DefaultSelected: true,
		},
		&Rule{
			ID:       "windows.directx-shader-cache",
			Name:     "DirectX shader cache",
			Category: CategoryWindows,
			Roots:    []string{`{LocalAppData}\D3DSCache`},
			What:     "Compiled graphics shaders cached by DirectX.",
			WhySafe: "The cache is rebuilt automatically from the game or app's own shaders. Windows Disk Cleanup " +
				"offers the same cleanup. Caches of running games are in use and skipped.",
			Impact:          "Games and 3D apps may stutter briefly the first time they run while shaders recompile.",
			DefaultSelected: true,
		},
		&Rule{
			ID:       "logs.windows-error-reports",
			Name:     "Windows error reports",
			Category: CategoryLogs,
			Roots: []string{
				`{LocalAppData}\Microsoft\Windows\WER\ReportArchive`,
				`{LocalAppData}\Microsoft\Windows\WER\ReportQueue`,
			},
			What:    "Crash and hang reports created by Windows Error Reporting for your account.",
			WhySafe: "Reports are diagnostic copies. Windows Disk Cleanup removes the same files.",
			Impact: "Queued reports will not be sent to Microsoft and past reports disappear from " +
				"Reliability Monitor's details.",
			DefaultSelected: true,
		},
	)
}
