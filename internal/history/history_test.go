package history_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harshul1484/out-of-windows/internal/history"
	"github.com/Harshul1484/out-of-windows/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

func TestAppendLoadNewestFirst(t *testing.T) {
	dir := testutil.Dir(t)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, cmd := range []string{"clean", "installer", "purge"} {
		if err := history.Append(dir, history.Record{Time: base.Add(time.Duration(i) * time.Minute), Command: cmd, Reclaimed: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := history.Load(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Command != "purge" || recs[1].Command != "installer" {
		t.Errorf("records = %+v", recs)
	}
}

func TestLoadSkipsMalformedLines(t *testing.T) {
	dir := testutil.Dir(t)
	if err := history.Append(dir, history.Record{Time: time.Now(), Command: "clean"}); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(dir, history.FileName), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("{\"time\": \"trunc\n")
	f.Close()
	recs, err := history.Load(dir, 0)
	if err != nil || len(recs) != 1 {
		t.Fatalf("recs = %v, err = %v", recs, err)
	}
}

func TestChangesRoundTripAndOldRecordsStillLoad(t *testing.T) {
	dir := testutil.Dir(t)
	// A record written before Changes existed.
	if err := os.WriteFile(filepath.Join(dir, history.FileName),
		[]byte(`{"time":"2026-09-01T10:00:00Z","command":"clean","removed":3,"reclaimed_bytes":10,"skipped":0,"errors":0,"duration_ms":5}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := history.Record{Time: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Command: "startup",
		Changes: []history.Change{{ID: "hkcu-run:App", Name: "App", Action: "disabled", Status: "changed", Detail: "before: 020000000000000000000000"}}}
	if err := history.Append(dir, rec); err != nil {
		t.Fatal(err)
	}
	recs, err := history.Load(dir, 0)
	if err != nil || len(recs) != 2 {
		t.Fatalf("recs = %v, err = %v", recs, err)
	}
	if len(recs[0].Changes) != 1 || recs[0].Changes[0] != rec.Changes[0] {
		t.Errorf("changes = %+v", recs[0].Changes)
	}
	if recs[1].Command != "clean" || recs[1].Changes != nil || recs[1].Removed != 3 {
		t.Errorf("old record = %+v", recs[1])
	}
}

func TestLoadMissingFile(t *testing.T) {
	recs, err := history.Load(testutil.Dir(t), 0)
	if err != nil || recs != nil {
		t.Fatalf("recs = %v, err = %v", recs, err)
	}
}

func TestDisabled(t *testing.T) {
	t.Setenv("OOW_NO_OPLOG", "1")
	dir := testutil.Dir(t)
	if err := history.Append(dir, history.Record{Time: time.Now(), Command: "clean"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, history.FileName)); !os.IsNotExist(err) {
		t.Fatal("history written while disabled")
	}
}
