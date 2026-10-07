package config

import (
	"sort"

	"github.com/Harshul1484/out-of-windows/internal/safety"
)

// AddPurgePath adds a folder to scan for project artifacts. It returns false
// if the folder is already listed.
func (c *Config) AddPurgePath(p string) (bool, error) {
	n, err := safety.Normalize(p)
	if err != nil {
		return false, err
	}
	for _, existing := range c.Purge.Paths {
		if e, err := safety.Normalize(existing); err == nil && safety.Key(e) == safety.Key(n) {
			return false, nil
		}
	}
	c.Purge.Paths = append(c.Purge.Paths, n)
	sort.Strings(c.Purge.Paths)
	return true, nil
}

// RemovePurgePath removes a folder from the purge paths. It returns false if
// it was not listed.
func (c *Config) RemovePurgePath(p string) bool {
	n, err := safety.Normalize(p)
	if err != nil {
		return false
	}
	for i, existing := range c.Purge.Paths {
		if e, err := safety.Normalize(existing); err == nil && safety.Key(e) == safety.Key(n) {
			c.Purge.Paths = append(c.Purge.Paths[:i], c.Purge.Paths[i+1:]...)
			return true
		}
	}
	return false
}
