package cleanup

func init() {
	register(
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
		&Rule{
			ID:       "logs.windows-error-reports-system",
			Name:     "System error reports",
			Category: CategoryLogs,
			Roots: []string{
				`{ProgramData}\Microsoft\Windows\WER\ReportArchive`,
				`{ProgramData}\Microsoft\Windows\WER\ReportQueue`,
			},
			What:    "Crash and hang reports created by Windows Error Reporting for all users and services.",
			WhySafe: "Reports are diagnostic copies. Windows Disk Cleanup removes the same files.",
			Impact: "Queued reports will not be sent to Microsoft and past reports disappear from " +
				"Reliability Monitor's details.",
			RequiresAdmin:   true,
			DefaultSelected: true,
		},
		&Rule{
			ID:       "logs.crash-dumps",
			Name:     "Application crash dumps",
			Category: CategoryLogs,
			Roots:    []string{`{LocalAppData}\CrashDumps`},
			What:     "Memory dumps written when applications crash.",
			WhySafe:  "Dumps are only used to diagnose past crashes; nothing reads them during normal use.",
			Impact:   "Developers or support staff can no longer analyse those past crashes.",
		},
		&Rule{
			ID:            "logs.minidumps",
			Name:          "System crash minidumps",
			Category:      CategoryLogs,
			Roots:         []string{`{Windows}\Minidump`},
			What:          "Small memory dumps Windows writes after a blue-screen crash.",
			WhySafe:       "They are only used to diagnose past system crashes; Windows Disk Cleanup removes them too.",
			Impact:        "Past blue-screen crashes can no longer be analysed.",
			RequiresAdmin: true,
		},
	)
}
