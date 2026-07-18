package domain

import "testing"

func TestPreflightOK(t *testing.T) {
	plan := AssemblyPlan{
		Added:    []PlannedComponent{{RepoName: "a", Version: "1"}},
		Checksum: "abc123",
	}
	if err := Preflight(plan); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPreflightMissingChecksum(t *testing.T) {
	err := Preflight(AssemblyPlan{Added: []PlannedComponent{{RepoName: "a"}}})
	assertCode(t, err, ErrPreflight)
}

func TestPreflightAddRemoveConflict(t *testing.T) {
	plan := AssemblyPlan{
		Added:    []PlannedComponent{{RepoName: "a"}},
		Removed:  []PlannedComponent{{RepoName: "a"}},
		Checksum: "c",
	}
	assertCode(t, Preflight(plan), ErrConflict)
}

func TestPreflightDependsOnRemoved(t *testing.T) {
	plan := AssemblyPlan{
		Added:    []PlannedComponent{{RepoName: "a", DependsOn: []string{"b"}}},
		Removed:  []PlannedComponent{{RepoName: "b"}},
		Checksum: "c",
	}
	assertCode(t, Preflight(plan), ErrConflict)
}

func TestTopologicalLayersOrdering(t *testing.T) {
	comps := []PlannedComponent{
		{RepoName: "app", DependsOn: []string{"db", "cache"}},
		{RepoName: "db"},
		{RepoName: "cache", DependsOn: []string{"db"}},
	}
	layers, err := TopologicalLayers(comps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("expected 3 layers, got %d: %+v", len(layers), layers)
	}
	if layers[0][0].RepoName != "db" {
		t.Fatalf("expected db first, got %s", layers[0][0].RepoName)
	}
	if layers[1][0].RepoName != "cache" {
		t.Fatalf("expected cache second, got %s", layers[1][0].RepoName)
	}
	if layers[2][0].RepoName != "app" {
		t.Fatalf("expected app last, got %s", layers[2][0].RepoName)
	}
}

func TestTopologicalLayersParallelLayer(t *testing.T) {
	comps := []PlannedComponent{
		{RepoName: "x"},
		{RepoName: "y"},
	}
	layers, err := TopologicalLayers(comps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 1 || len(layers[0]) != 2 {
		t.Fatalf("expected a single 2-component layer, got %+v", layers)
	}
}

func TestTopologicalLayersCycle(t *testing.T) {
	comps := []PlannedComponent{
		{RepoName: "a", DependsOn: []string{"b"}},
		{RepoName: "b", DependsOn: []string{"a"}},
	}
	_, err := TopologicalLayers(comps)
	assertCode(t, err, ErrConflict)
}

func TestSummary(t *testing.T) {
	plan := AssemblyPlan{
		Added:   []PlannedComponent{{RepoName: "a"}, {RepoName: "b"}},
		Reused:  []PlannedComponent{{RepoName: "c"}},
		Removed: []PlannedComponent{{RepoName: "d"}},
	}
	if got := Summary(plan); got != "2 added, 1 reused, 1 removed" {
		t.Fatalf("unexpected summary: %q", got)
	}
}

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	pe, ok := err.(*ProvisionError)
	if !ok {
		t.Fatalf("expected *ProvisionError, got %T", err)
	}
	if pe.Code != want {
		t.Fatalf("expected code %s, got %s", want, pe.Code)
	}
}
