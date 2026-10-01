package cleanup_test

import (
	"os/exec"
)

func execIcacls(args ...string) ([]byte, error) {
	return exec.Command("icacls", args...).CombinedOutput()
}
