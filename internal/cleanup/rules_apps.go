package cleanup

import "fmt"

// electron builds a cache rule for an Electron or WebView2 app whose data
// folder is base. Only Cache, Code Cache and GPUCache (plus extra cache
// folders named by the caller) are targeted; Local Storage, IndexedDB,
// Session Storage, databases, blob_storage and settings are never roots.
func electron(id, app string, bases []string, exes []string, extra ...string) *Rule {
	var roots []string
	for _, b := range bases {
		roots = append(roots, b+`\Cache`, b+`\Code Cache`, b+`\GPUCache`)
		for _, e := range extra {
			roots = append(roots, b+`\`+e)
		}
	}
	return &Rule{
		ID:              "apps." + id + ".cache",
		Name:            app + " cache",
		Category:        CategoryApps,
		App:             app,
		Roots:           roots,
		AppProcesses:    exes,
		DetectPaths:     bases,
		What:            fmt.Sprintf(webCacheWhat, app),
		WhySafe:         fmt.Sprintf(webCacheSafe, app),
		Impact:          webCacheImpact,
		DefaultSelected: true,
	}
}

func init() {
	register(
		electron("discord", "Discord",
			[]string{`{RoamingAppData}\discord`, `{RoamingAppData}\discordptb`, `{RoamingAppData}\discordcanary`},
			[]string{"Discord.exe", "DiscordPTB.exe", "DiscordCanary.exe"}),
		electron("slack", "Slack", []string{`{RoamingAppData}\Slack`}, []string{"slack.exe"}),
		electron("teams-classic", "Microsoft Teams (classic)", []string{`{RoamingAppData}\Microsoft\Teams`}, []string{"Teams.exe"}),
		&Rule{
			ID:       "apps.teams.cache",
			Name:     "Microsoft Teams cache",
			Category: CategoryApps,
			App:      "Microsoft Teams",
			Roots: []string{
				`{LocalAppData}\Packages\MSTeams_8wekyb3d8bbwe\LocalCache\Microsoft\MSTeams\EBWebView\{profile}\Cache`,
				`{LocalAppData}\Packages\MSTeams_8wekyb3d8bbwe\LocalCache\Microsoft\MSTeams\EBWebView\{profile}\Code Cache`,
				`{LocalAppData}\Packages\MSTeams_8wekyb3d8bbwe\LocalCache\Microsoft\MSTeams\EBWebView\{profile}\GPUCache`,
			},
			ProfileMarker:   "Preferences",
			AppProcesses:    []string{"ms-teams.exe"},
			DetectPaths:     []string{`{LocalAppData}\Packages\MSTeams_8wekyb3d8bbwe`},
			What:            fmt.Sprintf(webCacheWhat, "the new Microsoft Teams"),
			WhySafe:         fmt.Sprintf(webCacheSafe, "Teams"),
			Impact:          webCacheImpact,
			DefaultSelected: true,
		},
		electron("vscode", "Visual Studio Code",
			[]string{`{RoamingAppData}\Code`, `{RoamingAppData}\Code - Insiders`},
			[]string{"Code.exe", "Code - Insiders.exe"},
			"CachedData", "CachedExtensionVSIXs"),
		electron("cursor", "Cursor", []string{`{RoamingAppData}\Cursor`}, []string{"Cursor.exe"},
			"CachedData", "CachedExtensionVSIXs"),
		&Rule{
			ID:              "apps.spotify.cache",
			Name:            "Spotify web cache",
			Category:        CategoryApps,
			App:             "Spotify",
			Roots:           []string{`{LocalAppData}\Spotify\Browser\Cache`, `{LocalAppData}\Spotify\Browser\GPUCache`},
			AppProcesses:    []string{"Spotify.exe"},
			DetectPaths:     []string{`{LocalAppData}\Spotify`},
			What:            "Spotify's embedded browser cache (artwork, pages and scripts).",
			WhySafe:         "Downloaded copies of interface content that Spotify fetches again. Downloaded songs and settings are not touched.",
			Impact:          webCacheImpact,
			DefaultSelected: true,
		},
		&Rule{
			ID:       "apps.steam.htmlcache",
			Name:     "Steam web cache",
			Category: CategoryApps,
			App:      "Steam",
			Roots:    []string{`{LocalAppData}\Steam\htmlcache`},
			AppProcesses: []string{
				"steam.exe", "steamwebhelper.exe",
			},
			DetectPaths:     []string{`{LocalAppData}\Steam`},
			What:            "The Steam client's embedded browser cache (store and community pages).",
			WhySafe:         "Cached web content that Steam downloads again. Games, saves and settings are elsewhere and not touched.",
			Impact:          webCacheImpact,
			DefaultSelected: true,
		},
		&Rule{
			ID:       "apps.epic.webcache",
			Name:     "Epic Games Launcher web cache",
			Category: CategoryApps,
			App:      "Epic Games Launcher",
			Roots: []string{
				`{LocalAppData}\EpicGamesLauncher\Saved\webcache`,
				`{LocalAppData}\EpicGamesLauncher\Saved\webcache_4147`,
				`{LocalAppData}\EpicGamesLauncher\Saved\webcache_4430`,
			},
			AppProcesses:    []string{"EpicGamesLauncher.exe", "EpicWebHelper.exe"},
			DetectPaths:     []string{`{LocalAppData}\EpicGamesLauncher`},
			What:            "The Epic Games Launcher's embedded browser cache.",
			WhySafe:         "Cached store pages and images that the launcher downloads again. Games and saves are not touched.",
			Impact:          webCacheImpact,
			DefaultSelected: true,
		},
		&Rule{
			ID:              "apps.battlenet.cache",
			Name:            "Battle.net web cache",
			Category:        CategoryApps,
			App:             "Battle.net",
			Roots:           []string{`{LocalAppData}\Battle.net\BrowserCaches`},
			AppProcesses:    []string{"Battle.net.exe"},
			DetectPaths:     []string{`{LocalAppData}\Battle.net`},
			What:            "The Battle.net app's embedded browser cache.",
			WhySafe:         "Cached web content that Battle.net downloads again. Games and settings are not touched.",
			Impact:          webCacheImpact,
			DefaultSelected: true,
		},
		&Rule{
			ID:              "apps.adobe.camera-raw-cache",
			Name:            "Adobe Camera Raw cache",
			Category:        CategoryApps,
			App:             "Adobe Photoshop / Lightroom / Bridge",
			Roots:           []string{`{LocalAppData}\Adobe\CameraRaw\Cache`},
			AppProcesses:    []string{"Photoshop.exe", "Lightroom.exe", "Adobe Bridge.exe"},
			DetectPaths:     []string{`{LocalAppData}\Adobe\CameraRaw`},
			What:            "Preview data Camera Raw caches for raw photos it has opened.",
			WhySafe:         "Camera Raw rebuilds previews from the original raw files, which are never touched. Edits are stored in the photos' sidecar files or catalogs.",
			Impact:          "Opening raw photos is slower once while previews are rebuilt.",
			DefaultSelected: true,
		},
		&Rule{
			ID:       "apps.adobe.media-cache",
			Name:     "Adobe media cache",
			Category: CategoryApps,
			App:      "Adobe Premiere Pro / After Effects / Media Encoder",
			Roots: []string{
				`{RoamingAppData}\Adobe\Common\Media Cache Files`,
				`{RoamingAppData}\Adobe\Common\Media Cache`,
			},
			AppProcesses: []string{"Adobe Premiere Pro.exe", "AfterFX.exe", "Adobe Media Encoder.exe", "Audition.exe"},
			DetectPaths:  []string{`{RoamingAppData}\Adobe\Common`},
			What:         "Conformed audio and indexed media that Adobe video apps generate for imported footage.",
			WhySafe:      "Adobe documents the media cache as safe to delete while the apps are closed; it is regenerated from the original media, which is never touched.",
			Impact:       "Projects take longer to open the first time while audio is conformed and media re-indexed.",
		},
		&Rule{
			ID:             "apps.jetbrains.caches",
			Name:           "JetBrains IDE caches and indexes",
			Category:       CategoryApps,
			App:            "JetBrains IDEs",
			Roots:          []string{`{LocalAppData}\JetBrains\{profile}\caches`, `{LocalAppData}\JetBrains\{profile}\index`},
			ProfileMarker:  "caches",
			ProfileExclude: []string{"Toolbox"},
			AppProcesses: []string{
				"idea64.exe", "pycharm64.exe", "webstorm64.exe", "goland64.exe", "clion64.exe", "rider64.exe",
				"phpstorm64.exe", "rubymine64.exe", "datagrip64.exe", "rustrover64.exe", "dataspell64.exe",
			},
			DetectPaths: []string{`{LocalAppData}\JetBrains`},
			What:        "Caches and project indexes of JetBrains IDEs (IntelliJ IDEA, PyCharm, WebStorm, Rider, ...), including old IDE versions.",
			WhySafe:     "Equivalent to File | Invalidate Caches. Settings, plugins, projects and local history are stored elsewhere and not touched.",
			Impact:      "The IDE re-indexes open projects on next start, which can take several minutes.",
		},
	)
}
