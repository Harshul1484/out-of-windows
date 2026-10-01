package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/cli"
)

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{
		"": 0, "4096": 4096, "1KB": 1024, "500MB": 500 << 20, "1GB": 1 << 30, "1.5 GB": 3 << 29, "2t": 2 << 40, "10b": 10,
	} {
		got, err := cli.ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"abc", "-1GB", "1XB"} {
		if _, err := cli.ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) accepted", bad)
		}
	}
}

func TestAnalyzeTextSummary(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("analyze")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, want := range []string{"Users", "Program Files", "Largest files", "links and junctions not followed"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestAnalyzeJSON(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("analyze", filepath.Join(e.root, "C", "Users"), "--json", "--depth", "2", "--top", "3")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var doc struct {
		Schema string `json:"schema"`
		Root   struct {
			Size     int64 `json:"size"`
			Files    int64 `json:"files"`
			Children []struct {
				Name     string            `json:"name"`
				Size     int64             `json:"size"`
				Children []json.RawMessage `json:"children"`
			} `json:"children"`
		} `json:"root"`
		Largest []struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
		} `json:"largest"`
		ScanStatus string `json:"scan_status"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != "oow.analyze/v1" || doc.Root.Size == 0 || len(doc.Root.Children) == 0 || doc.ScanStatus != "complete" {
		t.Fatalf("doc = %+v", doc)
	}
	var sum int64
	for _, c := range doc.Root.Children {
		sum += c.Size
	}
	if sum > doc.Root.Size {
		t.Errorf("children (%d) exceed parent (%d)", sum, doc.Root.Size)
	}
	if len(doc.Largest) != 3 || doc.Largest[0].Size < doc.Largest[1].Size {
		t.Errorf("largest = %+v", doc.Largest)
	}
}

func TestAnalyzeLargeMinSize(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("analyze", "--large", "--min-size", "4MB", "--json")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var doc struct {
		Schema string `json:"schema"`
		Files  []struct {
			Size int64 `json:"size"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != "oow.large/v1" {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
	if len(doc.Files) == 0 {
		t.Fatal("no large files found")
	}
	for _, f := range doc.Files {
		if f.Size < 4<<20 {
			t.Errorf("file of %d bytes below --min-size", f.Size)
		}
	}
}

func TestAnalyzeErrors(t *testing.T) {
	e := newEnv(t)
	if _, _, code := e.run("analyze", "--large", "--min-size", "lots"); code != cli.ExitUsage {
		t.Errorf("bad size: code = %d", code)
	}
	if _, _, code := e.run("analyze", filepath.Join(e.root, "does-not-exist")); code != cli.ExitError {
		t.Errorf("missing path: code = %d", code)
	}
	if _, _, code := e.run("analyze", filepath.Join(e.root, `C\Users\sandbox\AppData\Local\Temp\link-to-documents`)); code != cli.ExitError {
		t.Errorf("junction root: code = %d", code)
	}
}
