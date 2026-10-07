// Command wingetcheck validates winget manifests against the official JSON
// schemas of the manifest type and version each file declares, and prints the
// parsed manifests as a JSON array (in argument order) for further checks.
//
//	go run -C tools ./wingetcheck <manifest.yaml>...
//
// The schemas are fetched from microsoft/winget-cli at a pinned commit, so a
// run never depends on a moving branch. Exit status 1 means a file failed.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// schemaCommit is the microsoft/winget-cli commit the schemas are read from:
// the last change to schemas/JSON/manifests/v1.10.0 (the version the
// templates in packaging/winget declare). A template that moves to a newer
// manifest version needs a commit that contains that version's schemas.
const schemaCommit = "b49f784e4b1d5bf2b8aa85dfbe3ba5d360b6be12"

var (
	manifestTypes = map[string]bool{"version": true, "installer": true, "defaultLocale": true, "locale": true, "singleton": true}
	versionRE     = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	client        = &http.Client{Timeout: 60 * time.Second}
	schemas       = map[string]*jsonschema.Schema{}
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: wingetcheck <manifest.yaml>...")
		os.Exit(2)
	}
	docs := []any{}
	failed := false
	for _, path := range os.Args[1:] {
		doc, err := check(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed = true
			continue
		}
		fmt.Fprintf(os.Stderr, "%s: valid %s manifest %s\n", path, doc.(map[string]any)["ManifestType"], doc.(map[string]any)["ManifestVersion"])
		docs = append(docs, doc)
	}
	if failed {
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(docs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// check parses one manifest and validates it against its declared schema.
func check(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	// Round-trip through JSON so the instance holds JSON types only.
	j, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("cannot be represented as JSON: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
	if err != nil {
		return nil, err
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("not a YAML mapping")
	}
	typ, _ := m["ManifestType"].(string)
	ver, _ := m["ManifestVersion"].(string)
	if !manifestTypes[typ] {
		return nil, fmt.Errorf("unknown ManifestType %q", typ)
	}
	if !versionRE.MatchString(ver) {
		return nil, fmt.Errorf("ManifestVersion %q is not a version like 1.10.0", ver)
	}
	sch, err := schema(typ, ver)
	if err != nil {
		return nil, err
	}
	if err := sch.Validate(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// schema returns the compiled official schema for a manifest type and version.
func schema(typ, ver string) (*jsonschema.Schema, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/microsoft/winget-cli/%s/schemas/JSON/manifests/v%s/manifest.%s.%s.json",
		schemaCommit, ver, typ, ver)
	if s, ok := schemas[url]; ok {
		return s, nil
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching the schema: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("no %s schema for manifest version %s at winget-cli commit %s (HTTP %d): update schemaCommit in tools/wingetcheck",
			typ, ver, schemaCommit[:12], resp.StatusCode)
	}
	doc, err := jsonschema.UnmarshalJSON(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("reading the schema %s: %w", url, err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	s, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("compiling the schema %s: %w", url, err)
	}
	schemas[url] = s
	return s, nil
}
