package safety

import (
	"fmt"
	"regexp"
	"strings"
)

// Locations describes where Windows keeps system and user data on this
// machine. Production code fills it from the Known Folder APIs
// (DiscoverLocations); tests build it by hand from a fixture directory.
//
// Empty fields mean "not present on this machine" and are ignored.
type Locations struct {
	SystemDrive string // C:\

	// System-owned trees.
	Windows          string
	ProgramFiles     string
	ProgramFilesX86  string
	ProgramData      string
	UsersRoot        string // C:\Users
	PublicProfile    string // C:\Users\Public
	WindowsTemp      string // C:\Windows\Temp
	CommonFilesExtra []string

	// Current user.
	UserProfile    string
	RoamingAppData string
	LocalAppData   string
	LocalLow       string
	Temp           string

	// User content folders: never touched by automatic cleanup.
	UserContent []string

	// Additional exact locations that must never be removed themselves
	// (Start Menu, Startup, ...).
	CriticalExtra []string

	// Fixed drive roots present on the machine, e.g. C:\ and D:\.
	FixedDrives []string

	// The tool's own configuration and data directories.
	SelfDirs []string
}

var templateToken = regexp.MustCompile(`\{([A-Za-z]+)\}`)

// Expand resolves a rule path template such as `{LocalAppData}\D3DSCache`.
// Unknown or unavailable tokens are an error, never an empty substitution,
// because an empty substitution could turn a narrow path into a broad one.
func (l Locations) Expand(template string) (string, error) {
	var expandErr error
	out := templateToken.ReplaceAllStringFunc(template, func(tok string) string {
		name := tok[1 : len(tok)-1]
		v, ok := l.token(name)
		if !ok {
			expandErr = fmt.Errorf("unknown location token %s", tok)
			return ""
		}
		if v == "" {
			expandErr = fmt.Errorf("location %s is not available on this system", tok)
			return ""
		}
		return strings.TrimRight(v, `\/`)
	})
	if expandErr != nil {
		return "", expandErr
	}
	return Normalize(out)
}

func (l Locations) token(name string) (string, bool) {
	switch name {
	case "Windows":
		return l.Windows, true
	case "WindowsTemp":
		return l.WindowsTemp, true
	case "ProgramData":
		return l.ProgramData, true
	case "UserProfile":
		return l.UserProfile, true
	case "RoamingAppData":
		return l.RoamingAppData, true
	case "LocalAppData":
		return l.LocalAppData, true
	case "LocalLow":
		return l.LocalLow, true
	case "Temp":
		return l.Temp, true
	}
	return "", false
}
