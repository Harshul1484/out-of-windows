package apps

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// System discovers apps installed on the running Windows system. It only
// reads the registry, package metadata and Get-AppxPackage output.
type System struct {
	// SkipAppX disables Microsoft Store discovery (it starts PowerShell).
	SkipAppX bool
}

// List discovers all apps concurrently.
func (s System) List(ctx context.Context) (*Inventory, error) {
	inv := &Inventory{PackageManagers: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	add := func(apps []App, warnings ...string) {
		mu.Lock()
		defer mu.Unlock()
		inv.Apps = append(inv.Apps, apps...)
		inv.Warnings = append(inv.Warnings, warnings...)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		apps, warnings := readRegistry()
		add(apps, warnings...)
	}()
	if !s.SkipAppX {
		wg.Add(1)
		go func() {
			defer wg.Done()
			apps, err := readAppX(ctx)
			if err != nil {
				add(nil, err.Error())
				return
			}
			add(apps)
		}()
	}

	home, _ := os.UserHomeDir()
	scoopRoot := firstNonEmpty(os.Getenv("SCOOP"), filepath.Join(home, "scoop"))
	scoopGlobal := firstNonEmpty(os.Getenv("SCOOP_GLOBAL"), filepath.Join(os.Getenv("ProgramData"), "scoop"))
	chocoRoot := firstNonEmpty(os.Getenv("ChocolateyInstall"), filepath.Join(os.Getenv("ProgramData"), "chocolatey"))
	add(readScoop(scoopRoot, ScopeUser))
	add(readScoop(scoopGlobal, ScopeMachine))
	add(readChocolatey(chocoRoot))
	wg.Wait()

	for _, pm := range []string{"winget", "scoop", "choco"} {
		if p, err := exec.LookPath(pm); err == nil {
			inv.PackageManagers[pm] = p
		}
	}
	SortByName(inv.Apps)
	return inv, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
