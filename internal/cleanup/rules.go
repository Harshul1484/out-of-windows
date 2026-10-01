package cleanup

import "time"

const (
	hour = time.Hour
	day  = 24 * time.Hour
)

// registry holds every built-in rule. Rule files (rules_*.go) register
// themselves from init functions, one file per category.
var registry []*Rule

func register(rules ...*Rule) { registry = append(registry, rules...) }

// BuiltinRules returns all built-in rules in display order.
func BuiltinRules() []*Rule {
	out := make([]*Rule, 0, len(registry))
	for _, c := range CategoryOrder {
		for _, r := range registry {
			if r.Category == c {
				out = append(out, r)
			}
		}
	}
	return out
}

// FindRule returns the built-in rule with the given ID.
func FindRule(id string) *Rule {
	for _, r := range registry {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// Explanations shared by the many Chromium/Electron cache rules.
const (
	webCacheWhat = "HTTP cache, compiled script cache and GPU cache of %s."
	webCacheSafe = "These are disposable copies of downloaded and compiled content. %s rebuilds them " +
		"automatically. Profiles, logins, cookies, history, settings and extensions are not touched."
	webCacheImpact = "Pages and the app may load a little slower the first time while the cache refills."
)
