package cleanup

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
	)
}
