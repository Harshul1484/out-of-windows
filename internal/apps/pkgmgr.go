package apps

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
)

// readScoop lists Scoop apps from <root>\apps\<name>\current\manifest.json.
func readScoop(root string, scope Scope) []App {
	entries, err := os.ReadDir(filepath.Join(root, "apps"))
	if err != nil {
		return nil
	}
	var out []App
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.EqualFold(name, "scoop") {
			continue
		}
		dir := filepath.Join(root, "apps", name)
		var m struct {
			Version     string `json:"version"`
			Description string `json:"description"`
			Homepage    string `json:"homepage"`
		}
		data, err := os.ReadFile(filepath.Join(dir, "current", "manifest.json"))
		if err != nil || json.Unmarshal(data, &m) != nil {
			continue // not a complete installation
		}
		out = append(out, App{
			ID:              "scoop:" + string(scope) + ":" + name,
			Name:            name,
			Version:         m.Version,
			Publisher:       "Scoop",
			Source:          SourceScoop,
			Scope:           scope,
			InstallLocation: dir,
			PackageName:     name,
		})
	}
	return out
}

// readChocolatey lists packages from <root>\lib\<id>\<id>.nuspec, skipping
// Chocolatey itself and its extension packages.
func readChocolatey(root string) []App {
	entries, err := os.ReadDir(filepath.Join(root, "lib"))
	if err != nil {
		return nil
	}
	var out []App
	for _, e := range entries {
		id := e.Name()
		lower := strings.ToLower(id)
		if !e.IsDir() || lower == "chocolatey" || strings.HasSuffix(lower, ".extension") ||
			strings.HasPrefix(lower, "chocolatey-") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "lib", id, id+".nuspec"))
		if err != nil {
			continue
		}
		var n struct {
			Metadata struct {
				ID      string `xml:"id"`
				Version string `xml:"version"`
				Title   string `xml:"title"`
				Authors string `xml:"authors"`
			} `xml:"metadata"`
		}
		if xml.Unmarshal(data, &n) != nil {
			continue
		}
		name := n.Metadata.Title
		if name == "" {
			name = id
		}
		out = append(out, App{
			ID:          "choco:" + id,
			Name:        name,
			Version:     n.Metadata.Version,
			Publisher:   n.Metadata.Authors,
			Source:      SourceChoco,
			Scope:       ScopeMachine,
			PackageName: id,
		})
	}
	return out
}
