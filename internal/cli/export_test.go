package cli

// Test hooks. They exist only in test binaries.

// SetUpdateAPIBase points `update` at a test server.
func SetUpdateAPIBase(u string) (restore func()) {
	old := updateAPIBase
	updateAPIBase = u
	return func() { updateAPIBase = old }
}

// SetSelfExe moves the simulated executable (sandbox mode only), e.g. to
// where a package manager would have put it.
func SetSelfExe(p string) (restore func()) {
	old := selfExeOverride
	selfExeOverride = p
	return func() { selfExeOverride = old }
}
