package cleanup

import "fmt"

// chromium builds a cache rule for a Chromium-based browser. Only the HTTP,
// code and GPU caches inside real profiles (folders holding a Preferences
// file) and browser-wide shader caches are targeted. Cookies, Login Data,
// History, Bookmarks, extensions, Service Worker storage, IndexedDB and Local
// Storage live next to them and are never roots.
func chromium(id, app, userData string, exes ...string) *Rule {
	return &Rule{
		ID:       "browser." + id + ".cache",
		Name:     app + " cache",
		Category: CategoryBrowser,
		App:      app,
		Roots: []string{
			userData + `\{profile}\Cache`,
			userData + `\{profile}\Code Cache`,
			userData + `\{profile}\GPUCache`,
			userData + `\ShaderCache`,
			userData + `\GrShaderCache`,
			userData + `\GraphiteDawnCache`,
		},
		ProfileMarker:   "Preferences",
		AppProcesses:    exes,
		DetectPaths:     []string{userData},
		What:            fmt.Sprintf(webCacheWhat, app),
		WhySafe:         fmt.Sprintf(webCacheSafe, app),
		Impact:          webCacheImpact,
		DefaultSelected: true,
	}
}

func init() {
	register(
		chromium("chrome", "Google Chrome", `{LocalAppData}\Google\Chrome\User Data`, "chrome.exe"),
		chromium("chrome-beta", "Google Chrome Beta", `{LocalAppData}\Google\Chrome Beta\User Data`, "chrome.exe"),
		chromium("chrome-canary", "Google Chrome Canary", `{LocalAppData}\Google\Chrome SxS\User Data`, "chrome.exe"),
		chromium("chromium", "Chromium", `{LocalAppData}\Chromium\User Data`, "chrome.exe"),
		chromium("edge", "Microsoft Edge", `{LocalAppData}\Microsoft\Edge\User Data`, "msedge.exe"),
		chromium("brave", "Brave", `{LocalAppData}\BraveSoftware\Brave-Browser\User Data`, "brave.exe"),
		chromium("vivaldi", "Vivaldi", `{LocalAppData}\Vivaldi\User Data`, "vivaldi.exe"),
		&Rule{
			ID:       "browser.opera.cache",
			Name:     "Opera cache",
			Category: CategoryBrowser,
			App:      "Opera",
			Roots: []string{
				`{LocalAppData}\Opera Software\Opera Stable\Cache`,
				`{LocalAppData}\Opera Software\Opera GX Stable\Cache`,
			},
			AppProcesses:    []string{"opera.exe"},
			DetectPaths:     []string{`{LocalAppData}\Opera Software`},
			What:            fmt.Sprintf(webCacheWhat, "Opera and Opera GX"),
			WhySafe:         fmt.Sprintf(webCacheSafe, "Opera"),
			Impact:          webCacheImpact,
			DefaultSelected: true,
		},
		&Rule{
			ID:       "browser.firefox.cache",
			Name:     "Firefox cache",
			Category: CategoryBrowser,
			App:      "Firefox",
			// The local profile folder only holds caches; the profile itself
			// (logins, cookies, history, bookmarks) lives under Roaming.
			Roots: []string{
				`{LocalAppData}\Mozilla\Firefox\Profiles\{profile}\cache2`,
				`{LocalAppData}\Mozilla\Firefox\Profiles\{profile}\startupCache`,
				`{LocalAppData}\Mozilla\Firefox\Profiles\{profile}\thumbnails`,
			},
			ProfileMarker: "cache2",
			AppProcesses:  []string{"firefox.exe"},
			DetectPaths:   []string{`{LocalAppData}\Mozilla\Firefox\Profiles`},
			What:          "HTTP cache, startup cache and page thumbnails of Firefox.",
			WhySafe: "These are disposable copies of downloaded content kept in Firefox's local (non-roaming) profile " +
				"folder. Logins, cookies, history, bookmarks and add-ons are stored elsewhere and are not touched.",
			Impact:          webCacheImpact,
			DefaultSelected: true,
		},
	)
}
