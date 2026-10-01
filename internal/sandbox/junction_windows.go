package sandbox

import (
	"fmt"
	"os"
	"os/exec"
)

// MakeJunction creates a directory junction at link pointing to target.
// Junctions, unlike symbolic links, need no special privilege. It is only used
// to build sandbox fixtures.
func MakeJunction(link, target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J: %v: %s", err, out)
	}
	return nil
}
