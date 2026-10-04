package cli_test

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Harshul1484/out-of-windows/internal/cli"
)

func TestStatusJSON(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("status", "--json", "--interval", "200ms")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var doc struct {
		Schema string `json:"schema"`
		CPU    struct {
			Percent float64   `json:"percent"`
			Cores   []float64 `json:"cores_percent"`
		} `json:"cpu"`
		Memory struct {
			UsedPercent float64 `json:"used_percent"`
		} `json:"memory"`
		GPUs      []struct{ Name string } `json:"gpus"`
		Processes struct {
			Count int `json:"count"`
			Top   []struct {
				Name string  `json:"name"`
				CPU  float64 `json:"cpu_percent"`
			} `json:"top_by_cpu"`
		} `json:"processes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != "oow.status/v1" || doc.CPU.Percent != 35 || len(doc.CPU.Cores) != 4 || doc.Memory.UsedPercent != 62.5 {
		t.Errorf("doc = %+v", doc)
	}
	if doc.Processes.Count != 4 || doc.Processes.Top[0].Name != "compiler.exe" || doc.Processes.Top[0].CPU != 20 {
		t.Errorf("processes = %+v", doc.Processes)
	}
}

func TestStatusWatchNDJSON(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("status", "--json", "--watch", "--count", "3", "--interval", "200ms")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	lines := 0
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var v map[string]any
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil || v["schema"] != "oow.status/v1" {
			t.Fatalf("line %d is not a status object: %s", lines, sc.Text())
		}
		lines++
	}
	if lines != 3 {
		t.Errorf("lines = %d, want 3", lines)
	}
	if _, _, code := e.run("status", "--interval", "10ms"); code != cli.ExitUsage {
		t.Errorf("tiny interval: code = %d", code)
	}
}

func TestStatusText(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("status", "--interval", "200ms")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, want := range []string{"SYSTEM STATUS", "CPU", "Memory", "GPU", "Disk", "Network", "TOP PROCESSES", "compiler.exe"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestProcesses(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.run("processes", "--json", "--sort", "memory", "--top", "2")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var doc struct {
		Schema    string `json:"schema"`
		Total     int    `json:"total"`
		Processes []struct {
			Name   string `json:"name"`
			Memory uint64 `json:"memory_bytes"`
		} `json:"processes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != "oow.processes/v1" || doc.Total != 4 || len(doc.Processes) != 2 || doc.Processes[0].Name != "browser.exe" {
		t.Errorf("doc = %+v", doc)
	}
	out, _, _ = e.run("processes", "--name", "EDIT", "--json")
	if !strings.Contains(out, "editor.exe") || strings.Contains(out, "browser.exe") {
		t.Errorf("name filter: %s", out)
	}
	if _, _, code := e.run("processes", "--sort", "colour"); code != cli.ExitUsage {
		t.Errorf("bad sort: code = %d", code)
	}
}
