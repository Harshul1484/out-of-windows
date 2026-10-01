// Package safety decides which filesystem paths may ever be deleted.
//
// Everything in this package that compares paths works on Windows path
// semantics implemented in pure Go, independent of the host OS, so the rules
// can be exhaustively unit-tested. OS-specific resolution (8.3 short names,
// junctions, final paths) happens in the filesystem package, which always
// hands this package the OS-resolved final path before a deletion.
package safety

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Path errors returned by Normalize.
var (
	ErrEmptyPath       = errors.New("empty path")
	ErrRelativePath    = errors.New("path is not absolute")
	ErrInvalidPath     = errors.New("path contains invalid characters")
	ErrUnsupportedPath = errors.New("unsupported path form")
)

// Normalize returns the canonical form of an absolute Windows path.
//
// It strips Win32 namespace prefixes (\\?\, \??\, \\.\), converts forward
// slashes, collapses repeated separators, resolves "." and "..", and trims
// trailing dots and spaces from every component the way Win32 path
// normalization does. The drive letter is upper-cased; the rest of the casing
// is preserved for display. Use Key to compare paths.
//
// Relative paths, drive-relative paths (C:foo), alternate data streams,
// wildcards and volume GUID / device paths are rejected rather than guessed at.
func Normalize(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", ErrEmptyPath
	}
	if strings.ContainsRune(p, 0) {
		return "", ErrInvalidPath
	}
	p = strings.ReplaceAll(p, "/", `\`)

	// Strip at most one namespace prefix.
	for _, prefix := range []string{`\\?\`, `\??\`, `\\.\`} {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := p[len(prefix):]
		switch {
		case len(rest) >= 4 && strings.EqualFold(rest[:4], `UNC\`):
			p = `\\` + rest[4:]
		case len(rest) >= 2 && isDriveLetter(rest[0]) && rest[1] == ':':
			p = rest
		default:
			// Volume GUIDs, GLOBALROOT, pipes, physical drives...
			return "", fmt.Errorf("%w: %q", ErrUnsupportedPath, p)
		}
		break
	}

	var root string
	var rest string
	switch {
	case strings.HasPrefix(p, `\\`):
		if strings.HasPrefix(p, `\\\`) {
			return "", fmt.Errorf("%w: malformed UNC path %q", ErrUnsupportedPath, p)
		}
		parts := splitNonEmpty(p[2:])
		if len(parts) < 2 {
			return "", fmt.Errorf("%w: incomplete UNC path %q", ErrUnsupportedPath, p)
		}
		for _, part := range parts[:2] {
			if part == "." || part == ".." || strings.ContainsAny(part, `:*?"<>|`) {
				return "", fmt.Errorf("%w: malformed UNC path %q", ErrUnsupportedPath, p)
			}
		}
		root = `\\` + parts[0] + `\` + parts[1]
		rest = strings.Join(parts[2:], `\`)
	case len(p) >= 2 && isDriveLetter(p[0]) && p[1] == ':':
		if len(p) == 2 || p[2] != '\\' {
			return "", fmt.Errorf("%w: drive-relative path %q", ErrRelativePath, p)
		}
		root = strings.ToUpper(p[:1]) + `:\`
		rest = p[3:]
	default:
		return "", fmt.Errorf("%w: %q", ErrRelativePath, p)
	}

	var out []string
	for _, c := range strings.Split(rest, `\`) {
		switch c {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		// Win32 ignores trailing dots and spaces in path components, so
		// "C:\Windows. \" names the same directory as "C:\Windows".
		c = strings.TrimRight(c, ". ")
		if c == "" {
			continue
		}
		if strings.ContainsAny(c, `:*?"<>|`) {
			return "", fmt.Errorf("%w: %q", ErrInvalidPath, p)
		}
		out = append(out, c)
	}

	if len(out) == 0 {
		return root, nil
	}
	if strings.HasSuffix(root, `\`) {
		return root + strings.Join(out, `\`), nil
	}
	return root + `\` + strings.Join(out, `\`), nil
}

// MustNormalize is Normalize for trusted constants; it panics on error.
func MustNormalize(p string) string {
	n, err := Normalize(p)
	if err != nil {
		panic(err)
	}
	return n
}

// Key returns a case-folded form of a normalized path for comparisons.
// NTFS compares names with a per-character upper-case table, which a
// rune-wise simple upper-case mapping approximates without changing length.
func Key(normalized string) string {
	return strings.Map(unicode.ToUpper, normalized)
}

// IsWithin reports whether child is parent or a descendant of parent.
// Both arguments must be normalized.
func IsWithin(child, parent string) bool {
	c, p := Key(child), Key(parent)
	if c == p {
		return true
	}
	if strings.HasSuffix(p, `\`) { // drive root "C:\"
		return strings.HasPrefix(c, p)
	}
	return strings.HasPrefix(c, p+`\`)
}

// IsStrictlyWithin reports whether child is a descendant of parent and not
// parent itself. Both arguments must be normalized.
func IsStrictlyWithin(child, parent string) bool {
	return IsWithin(child, parent) && Key(child) != Key(parent)
}

// IsVolumeRoot reports whether a normalized path is a drive root (C:\) or a
// UNC share root (\\server\share).
func IsVolumeRoot(normalized string) bool {
	if len(normalized) == 3 && normalized[1] == ':' && normalized[2] == '\\' {
		return true
	}
	if strings.HasPrefix(normalized, `\\`) {
		return len(splitNonEmpty(normalized[2:])) == 2
	}
	return false
}

// IsUNC reports whether a normalized path is a network (UNC) path.
func IsUNC(normalized string) bool {
	return strings.HasPrefix(normalized, `\\`)
}

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, part := range strings.Split(s, `\`) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
