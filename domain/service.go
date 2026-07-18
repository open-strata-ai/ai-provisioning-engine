package domain

import (
	"fmt"
	"sort"
)

// Preflight validates a plan before any change is applied (SKILLS §12 S1/S4).
// It rejects a missing checksum, add/remove conflicts, and dependencies on a
// component that is being removed.
func Preflight(plan AssemblyPlan) error {
	if plan.Checksum == "" {
		return NewError(ErrPreflight, "plan checksum is required")
	}
	added := make(map[string]bool, len(plan.Added))
	for _, c := range plan.Added {
		if c.RepoName == "" {
			return NewError(ErrInvalidRequest, "added component missing repo_name")
		}
		added[c.RepoName] = true
	}
	removed := make(map[string]bool, len(plan.Removed))
	for _, c := range plan.Removed {
		removed[c.RepoName] = true
	}
	for name := range added {
		if removed[name] {
			return NewError(ErrConflict, "component is both added and removed: "+name)
		}
	}
	for _, c := range plan.Added {
		for _, d := range c.DependsOn {
			if removed[d] {
				return NewError(ErrConflict,
					"component "+c.RepoName+" depends on removed component "+d)
			}
		}
	}
	return nil
}

// TopologicalLayers orders Added components so that in-set dependencies come
// first. Components within a layer have no intra-set dependency and may be
// deployed in parallel (SKILLS §9.5). Returns an error on a dependency cycle.
func TopologicalLayers(comps []PlannedComponent) ([][]PlannedComponent, error) {
	byName := make(map[string]PlannedComponent, len(comps))
	inSet := make(map[string]bool, len(comps))
	for _, c := range comps {
		byName[c.RepoName] = c
		inSet[c.RepoName] = true
	}

	indeg := make(map[string]int, len(comps))
	dependents := make(map[string][]string) // dep -> components depending on it
	for _, c := range comps {
		indeg[c.RepoName] = 0
	}
	for _, c := range comps {
		seen := map[string]bool{}
		for _, d := range c.DependsOn {
			if !inSet[d] || d == c.RepoName || seen[d] {
				continue // ignore external deps (assumed already running)
			}
			seen[d] = true
			indeg[c.RepoName]++
			dependents[d] = append(dependents[d], c.RepoName)
		}
	}

	done := make(map[string]bool, len(comps))
	var layers [][]PlannedComponent
	remaining := len(comps)
	for remaining > 0 {
		var names []string
		for name, d := range indeg {
			if d == 0 && !done[name] {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return nil, NewError(ErrConflict, "dependency cycle detected among added components")
		}
		sort.Strings(names) // deterministic ordering within a layer
		layer := make([]PlannedComponent, 0, len(names))
		for _, n := range names {
			layer = append(layer, byName[n])
			done[n] = true
			for _, dep := range dependents[n] {
				indeg[dep]--
			}
		}
		layers = append(layers, layer)
		remaining -= len(names)
	}
	return layers, nil
}

// Summary renders the human-readable apply summary (SPECS §7.2).
func Summary(plan AssemblyPlan) string {
	return fmt.Sprintf("%d added, %d reused, %d removed",
		len(plan.Added), len(plan.Reused), len(plan.Removed))
}
