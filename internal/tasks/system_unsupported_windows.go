//go:build !(amd64 || arm64)

package tasks

import "context"

// System is unavailable on this architecture: the COM calls in
// system_windows.go pass VARIANT arguments the way the x64 and ARM64 calling
// conventions do. Listing reports nothing rather than guessing.
type System struct{}

// List returns ErrUnsupported.
func (System) List(context.Context) ([]Task, []string, error) { return nil, nil, ErrUnsupported }

// SetEnabled returns ErrUnsupported.
func (System) SetEnabled(string, bool) error { return ErrUnsupported }

// Account returns an empty account.
func (System) Account() Account { return Account{} }

// CurrentAccount returns an empty account.
func CurrentAccount() Account { return Account{} }
