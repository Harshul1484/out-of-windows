package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Command is a parsed uninstall command line.
type Command struct {
	Exe  string
	Args []string
}

var msiProduct = regexp.MustCompile(`(?i)\{[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}\}`)

// IsMSIExec reports whether exe names the Windows Installer.
func IsMSIExec(exe string) bool {
	return strings.EqualFold(filepath.Base(exe), "msiexec.exe") || strings.EqualFold(exe, "msiexec")
}

// ProductCodeIn returns the MSI product code ({GUID}) in s, or "".
func ProductCodeIn(s string) string {
	return strings.ToUpper(msiProduct.FindString(s))
}

// ParseCommandLine splits a registered uninstall command. Registry values are
// often unquoted even when the path contains spaces ("C:\Program Files\App
// \uninstall.exe /S"), so when the first token is not an existing file,
// tokens are joined until one is. exists reports whether a file exists.
func ParseCommandLine(cmd string, exists func(string) bool) (Command, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return Command{}, fmt.Errorf("empty command")
	}
	if strings.HasPrefix(cmd, `"`) {
		end := strings.Index(cmd[1:], `"`)
		if end < 0 {
			return Command{}, fmt.Errorf("unbalanced quotes in %q", cmd)
		}
		return Command{Exe: cmd[1 : end+1], Args: splitArgs(cmd[end+2:])}, nil
	}
	fields := strings.Fields(cmd)
	if IsMSIExec(fields[0]) {
		return Command{Exe: fields[0], Args: splitArgs(strings.Join(fields[1:], " "))}, nil
	}
	for i := 1; i <= len(fields); i++ {
		candidate := strings.Join(fields[:i], " ")
		if exists(candidate) || exists(candidate+".exe") {
			if !strings.HasSuffix(strings.ToLower(candidate), ".exe") && exists(candidate+".exe") {
				candidate += ".exe"
			}
			return Command{Exe: candidate, Args: splitArgs(strings.Join(fields[i:], " "))}, nil
		}
	}
	// Nothing on disk matched: report the first token as the executable.
	return Command{Exe: fields[0], Args: splitArgs(strings.Join(fields[1:], " "))}, nil
}

// splitArgs splits arguments, keeping double-quoted parts together and
// dropping the quotes.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote, have := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			have = true
		case (r == ' ' || r == '\t') && !inQuote:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}

// FileExists reports whether p is an existing regular file.
func FileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
