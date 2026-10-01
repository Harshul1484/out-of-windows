package filesystem

import (
	"errors"
	"os"
	"testing"
)

// TestFenceBlocksDeletionOutside narrows the fence (set by TestMain) to one
// sandbox subdirectory and proves a fully-approved deletion in a sibling
// directory is refused below the policy layer.
func TestFenceBlocksDeletionOutside(t *testing.T) {
	base := Fence()
	if base == "" {
		t.Fatal("no fence set by TestMain")
	}
	inside, err := os.MkdirTemp(base, "inside-")
	if err != nil {
		t.Fatal(err)
	}
	outside, err := os.MkdirTemp(base, "outside-")
	if err != nil {
		t.Fatal(err)
	}
	victim := outside + `\victim.txt`
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Lstat(victim)
	if err != nil {
		t.Fatal(err)
	}

	old := fence.Load()
	narrowed, err := FinalPath(inside)
	if err != nil {
		t.Fatal(err)
	}
	fence.Store(&narrowed)
	defer fence.Store(old)

	err = RemoveVerified(victim, e.Fingerprint, func(string) error { return nil })
	if !errors.Is(err, ErrOutsideFence) {
		t.Fatalf("err = %v, want ErrOutsideFence", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("file outside the fence was deleted")
	}
}

func TestCheckFence(t *testing.T) {
	old := fence.Load()
	defer fence.Store(old)
	f := `D:\sandbox\run`
	fence.Store(&f)
	for p, ok := range map[string]bool{
		`D:\sandbox\run\a.txt`:   true,
		`D:\SANDBOX\RUN\x\y`:     true,
		`D:\sandbox\run`:         false, // the fence root itself
		`D:\sandbox\running\x`:   false,
		`D:\sandbox\other\a.txt`: false,
		`C:\Windows\x`:           false,
	} {
		if err := checkFence(p); (err == nil) != ok {
			t.Errorf("checkFence(%q) = %v, want ok=%v", p, err, ok)
		}
	}
}
