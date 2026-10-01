package cleanup

func init() {
	register(
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
			ID:       "windows.gpu-shader-cache.nvidia",
			Name:     "NVIDIA shader cache",
			Category: CategoryWindows,
			Roots: []string{
				`{LocalAppData}\NVIDIA\DXCache`,
				`{LocalAppData}\NVIDIA\GLCache`,
				`{LocalLow}\NVIDIA\PerDriverVersion\DXCache`,
				`{LocalLow}\NVIDIA\PerDriverVersion\GLCache`,
			},
			What:            "Shaders compiled by the NVIDIA driver for DirectX, OpenGL and Vulkan.",
			WhySafe:         "The driver recompiles shaders on demand; old entries from previous drivers are never reused. Files in use are skipped.",
			Impact:          "Games may stutter briefly while shaders recompile.",
			DefaultSelected: true,
		},
		&Rule{
			ID:       "windows.gpu-shader-cache.amd",
			Name:     "AMD shader cache",
			Category: CategoryWindows,
			Roots: []string{
				`{LocalAppData}\AMD\DxCache`,
				`{LocalAppData}\AMD\DxcCache`,
				`{LocalAppData}\AMD\Dx9Cache`,
				`{LocalAppData}\AMD\GLCache`,
				`{LocalAppData}\AMD\VkCache`,
			},
			What:            "Shaders compiled by the AMD driver for DirectX, OpenGL and Vulkan.",
			WhySafe:         "The driver recompiles shaders on demand. Files in use are skipped.",
			Impact:          "Games may stutter briefly while shaders recompile.",
			DefaultSelected: true,
		},
		&Rule{
			ID:              "windows.gpu-shader-cache.intel",
			Name:            "Intel shader cache",
			Category:        CategoryWindows,
			Roots:           []string{`{LocalLow}\Intel\ShaderCache`, `{LocalAppData}\Intel\ShaderCache`},
			What:            "Shaders compiled by the Intel graphics driver.",
			WhySafe:         "The driver recompiles shaders on demand. Files in use are skipped.",
			Impact:          "Games may stutter briefly while shaders recompile.",
			DefaultSelected: true,
		},
		&Rule{
			ID:       "windows.inetcache",
			Name:     "Temporary Internet Files",
			Category: CategoryWindows,
			Roots:    []string{`{LocalAppData}\Microsoft\Windows\INetCache`},
			// Outlook stores opened attachments under Content.Outlook; an edited
			// attachment saved there may be the user's only copy.
			Exclude: []string{"Content.Outlook", "Content.MSO"},
			MinAge:  24 * hour,
			What:    "The WinINet download cache used by Windows components, Office and older apps.",
			WhySafe: "Cached downloads are fetched again when needed; Windows Disk Cleanup removes the same files. " +
				"Outlook attachment and Office document caches are excluded.",
			Impact:          "Some web content in apps loads again from the network once.",
			DefaultSelected: true,
		},
		&Rule{
			ID:       "windows.thumbnail-cache",
			Name:     "Thumbnail cache",
			Category: CategoryWindows,
			Roots:    []string{`{LocalAppData}\Microsoft\Windows\Explorer`},
			Include:  []string{"thumbcache_*.db"},
			MaxDepth: 1,
			What:     "Picture and video thumbnails cached by File Explorer.",
			WhySafe:  "Explorer regenerates thumbnails when folders are opened. Explorer keeps the active cache files open; those are skipped.",
			Impact: "Folders with many pictures show placeholders briefly while thumbnails are rebuilt. " +
				"Most files can only be removed while Explorer is not running.",
		},
		&Rule{
			ID:       "windows.recycle-bin",
			Name:     "Recycle Bin",
			Category: CategoryWindows,
			Special:  SpecialRecycleBin,
			What:     "Everything in the Recycle Bin on all drives.",
			WhySafe:  "You already deleted these items. Emptying uses the Windows Shell, exactly like Empty Recycle Bin in Explorer.",
			Impact:   "Items in the Recycle Bin are permanently deleted and can no longer be restored.",
		},
	)
}
