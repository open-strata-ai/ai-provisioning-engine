package persistence

import (
	"testing"
	"time"

	"github.com/open-strata-ai/ai-provisioning-engine/domain"
)

func TestStoreRevisions(t *testing.T) {
	s := NewStore()
	_ = s.Save(domain.Record{PlanChecksum: "c1", Component: "a", Revision: "r1", Status: domain.StatusSuccess})
	_ = s.Save(domain.Record{PlanChecksum: "c2", Component: "a", Revision: "r2", Status: domain.StatusSuccess})
	_ = s.Save(domain.Record{PlanChecksum: "c3", Component: "a", Revision: "r2", Status: domain.StatusSuccess}) // dup

	revs := s.Revisions("a")
	if len(revs) != 2 || revs[0] != "r1" || revs[1] != "r2" {
		t.Fatalf("expected [r1 r2], got %v", revs)
	}
	last, ok := s.LastRevision("a")
	if !ok || last != "r2" {
		t.Fatalf("expected last r2, got %q ok=%v", last, ok)
	}
	if !s.HasRevision("a", "r1") || s.HasRevision("a", "nope") {
		t.Fatalf("HasRevision mismatch")
	}
}

func TestStoreByChecksum(t *testing.T) {
	s := NewStore()
	_ = s.Save(domain.Record{PlanChecksum: "c1", Component: "a"})
	_ = s.Save(domain.Record{PlanChecksum: "c1", Component: "b"})
	_ = s.Save(domain.Record{PlanChecksum: "c2", Component: "c"})
	if got := s.ByChecksum("c1"); len(got) != 2 {
		t.Fatalf("expected 2 records for c1, got %d", len(got))
	}
}

func TestLockerAcquireRelease(t *testing.T) {
	l := NewLocker()
	if !l.Acquire("k", time.Minute) {
		t.Fatal("first acquire should succeed")
	}
	if l.Acquire("k", time.Minute) {
		t.Fatal("second acquire should fail while held")
	}
	l.Release("k")
	if !l.Acquire("k", time.Minute) {
		t.Fatal("acquire after release should succeed")
	}
}

func TestLockerExpiry(t *testing.T) {
	l := NewLocker()
	now := time.Unix(0, 0)
	l.now = func() time.Time { return now }
	if !l.Acquire("k", time.Second) {
		t.Fatal("acquire should succeed")
	}
	now = now.Add(2 * time.Second) // past TTL
	if !l.Acquire("k", time.Second) {
		t.Fatal("acquire after expiry should succeed")
	}
}
