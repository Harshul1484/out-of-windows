package safety

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// DiscoverLocations reads well-known locations from the Known Folder and
// system directory APIs. These APIs honour folder redirection and are not
// affected by localized folder display names.
//
// Environment variables are only used to *add* protection (for example
// OneDrive roots), never as the source of a cleanup root.
func DiscoverLocations() Locations {
	kf := func(id *windows.KNOWNFOLDERID) string {
		p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT)
		if err != nil {
			return ""
		}
		return LongPath(p)
	}

	var l Locations
	if win, err := windows.GetSystemWindowsDirectory(); err == nil {
		l.Windows = LongPath(win)
	}
	if l.Windows == "" {
		l.Windows = kf(windows.FOLDERID_Windows)
	}
	if len(l.Windows) >= 3 && l.Windows[1] == ':' {
		l.SystemDrive = strings.ToUpper(l.Windows[:1]) + `:\`
	}
	if l.Windows != "" {
		l.WindowsTemp = l.Windows + `\Temp`
	}

	l.ProgramFiles = kf(windows.FOLDERID_ProgramFiles)
	l.ProgramFilesX86 = kf(windows.FOLDERID_ProgramFilesX86)
	l.ProgramData = kf(windows.FOLDERID_ProgramData)
	l.CommonFilesExtra = nonEmpty(
		kf(windows.FOLDERID_ProgramFilesCommon),
		kf(windows.FOLDERID_ProgramFilesCommonX86),
	)
	l.UsersRoot = kf(windows.FOLDERID_UserProfiles)
	l.PublicProfile = kf(windows.FOLDERID_Public)

	l.UserProfile = kf(windows.FOLDERID_Profile)
	l.RoamingAppData = kf(windows.FOLDERID_RoamingAppData)
	l.LocalAppData = kf(windows.FOLDERID_LocalAppData)
	l.LocalLow = kf(windows.FOLDERID_LocalAppDataLow)
	l.Temp = LongPath(os.TempDir())

	l.UserContent = nonEmpty(
		kf(windows.FOLDERID_Desktop),
		kf(windows.FOLDERID_Documents),
		kf(windows.FOLDERID_Downloads),
		kf(windows.FOLDERID_Pictures),
		kf(windows.FOLDERID_Music),
		kf(windows.FOLDERID_Videos),
		kf(windows.FOLDERID_Favorites),
		kf(windows.FOLDERID_SavedGames),
		kf(windows.FOLDERID_Contacts),
		kf(windows.FOLDERID_Links),
		kf(windows.FOLDERID_SkyDrive),
		kf(windows.FOLDERID_PublicDesktop),
		kf(windows.FOLDERID_PublicDocuments),
		kf(windows.FOLDERID_PublicDownloads),
		kf(windows.FOLDERID_PublicMusic),
		kf(windows.FOLDERID_PublicPictures),
		kf(windows.FOLDERID_PublicVideos),
		LongPath(os.Getenv("OneDrive")),
		LongPath(os.Getenv("OneDriveConsumer")),
		LongPath(os.Getenv("OneDriveCommercial")),
	)

	l.CriticalExtra = nonEmpty(
		kf(windows.FOLDERID_StartMenu),
		kf(windows.FOLDERID_Programs),
		kf(windows.FOLDERID_Startup),
		kf(windows.FOLDERID_CommonStartMenu),
		kf(windows.FOLDERID_CommonPrograms),
		kf(windows.FOLDERID_CommonStartup),
		kf(windows.FOLDERID_SendTo),
		kf(windows.FOLDERID_Templates),
		kf(windows.FOLDERID_UserProgramFiles),
		joinIf(l.LocalAppData, "Microsoft"),
		joinIf(l.LocalAppData, "Packages"),
		joinIf(l.RoamingAppData, "Microsoft"),
	)

	l.ShortcutRoots = nonEmpty(kf(windows.FOLDERID_Programs), kf(windows.FOLDERID_Desktop))

	l.FixedDrives = fixedDrives()
	return l
}

// LongPath expands 8.3 short names (C:\PROGRA~1) to their long form. Paths
// that do not exist are returned unchanged.
func LongPath(p string) string {
	if p == "" {
		return ""
	}
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetLongPathName(in, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return p
		}
		if int(n) <= len(buf) {
			return windows.UTF16ToString(buf[:n])
		}
		buf = make([]uint16, n)
	}
}

func fixedDrives() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		if windows.GetDriveType(p) == windows.DRIVE_FIXED {
			out = append(out, root)
		}
	}
	return out
}

func joinIf(base, name string) string {
	if base == "" {
		return ""
	}
	return base + `\` + name
}

func nonEmpty(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
