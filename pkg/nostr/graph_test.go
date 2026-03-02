package nostr

import (
	"testing"
)

func makeEvent(id, pubkey string, kind int, tags [][]string) *Event {
	return &Event{
		ID:        id,
		Pubkey:    pubkey,
		CreatedAt: 1700000000,
		Kind:      kind,
		Tags:      tags,
		Content:   "test",
	}
}

func TestEventGraphClustering(t *testing.T) {
	g := NewEventGraph()

	// Two events from author A, one from author B.
	g.Add(makeEvent("aaa1", "pubA", 1, nil))
	g.Add(makeEvent("aaa2", "pubA", 1, nil))
	g.Add(makeEvent("bbb1", "pubB", 7, nil))

	g.Resolve()

	s := g.Stats()
	if s.TotalEvents != 3 {
		t.Fatalf("expected 3 events, got %d", s.TotalEvents)
	}
	if s.AuthorClusters != 2 {
		t.Fatalf("expected 2 author clusters, got %d", s.AuthorClusters)
	}
	if s.KindClusters != 2 {
		t.Fatalf("expected 2 kind clusters (kind:1, kind:7), got %d", s.KindClusters)
	}
	if s.LargestAuthor != 2 {
		t.Fatalf("expected largest author cluster = 2, got %d", s.LargestAuthor)
	}
}

func TestEventGraphReferenceBonds(t *testing.T) {
	g := NewEventGraph()

	// Event bbb1 references event aaa1 via e-tag.
	g.Add(makeEvent("aaa1", "pubA", 1, nil))
	g.Add(makeEvent("bbb1", "pubB", 1, [][]string{{"e", "aaa1"}}))

	g.Resolve()

	s := g.Stats()
	if s.RefBonds != 1 {
		t.Fatalf("expected 1 resolved reference bond, got %d", s.RefBonds)
	}
	if s.Orphans != 0 {
		t.Fatalf("expected 0 orphans, got %d", s.Orphans)
	}

	// Verify the InRef on aaa1.
	node := g.Nodes["aaa1"]
	if len(node.InRefs) != 1 || node.InRefs[0] != "bbb1" {
		t.Fatalf("expected aaa1 to have InRef from bbb1, got %v", node.InRefs)
	}
}

func TestEventGraphOrphans(t *testing.T) {
	g := NewEventGraph()

	// Event references a nonexistent event and a nonexistent pubkey.
	g.Add(makeEvent("aaa1", "pubA", 1, [][]string{
		{"e", "missing_event_id"},
		{"p", "missing_pubkey"},
	}))

	g.Resolve()

	s := g.Stats()
	if s.Orphans != 2 {
		t.Fatalf("expected 2 orphans, got %d", s.Orphans)
	}

	byType := g.OrphansByType()
	if len(byType["event"]) != 1 {
		t.Fatalf("expected 1 event orphan, got %d", len(byType["event"]))
	}
	if len(byType["pubkey"]) != 1 {
		t.Fatalf("expected 1 pubkey orphan, got %d", len(byType["pubkey"]))
	}

	// Verify orphan details.
	eo := byType["event"][0]
	if eo.ID != "missing_event_id" || eo.RefBy != "aaa1" {
		t.Fatalf("event orphan mismatch: %+v", eo)
	}
	po := byType["pubkey"][0]
	if po.ID != "missing_pubkey" || po.RefBy != "aaa1" {
		t.Fatalf("pubkey orphan mismatch: %+v", po)
	}
}

func TestEventGraphDeduplicate(t *testing.T) {
	g := NewEventGraph()

	g.Add(makeEvent("aaa1", "pubA", 1, nil))
	g.Add(makeEvent("aaa1", "pubA", 1, nil)) // duplicate

	if len(g.Nodes) != 1 {
		t.Fatalf("expected 1 node after dedup, got %d", len(g.Nodes))
	}
	// Author cluster should have 1 event, not 2.
	if len(g.Authors["pubA"].Events) != 1 {
		t.Fatalf("expected 1 event in author cluster, got %d", len(g.Authors["pubA"].Events))
	}
}

func TestEventGraphPubkeyOrphanResolvedByPresence(t *testing.T) {
	g := NewEventGraph()

	// pubB mentions pubA via p-tag. pubA is present as an author.
	g.Add(makeEvent("aaa1", "pubA", 1, nil))
	g.Add(makeEvent("bbb1", "pubB", 1, [][]string{{"p", "pubA"}}))

	g.Resolve()

	s := g.Stats()
	// pubA is present as an author, so the p-tag reference is NOT an orphan.
	if s.Orphans != 0 {
		t.Fatalf("expected 0 orphans (pubA is present), got %d", s.Orphans)
	}
}

func TestEventGraphReplyChain(t *testing.T) {
	g := NewEventGraph()

	// A → B → C reply chain.
	g.Add(makeEvent("ev_a", "pubA", 1, nil))
	g.Add(makeEvent("ev_b", "pubB", 1, [][]string{{"e", "ev_a"}}))
	g.Add(makeEvent("ev_c", "pubC", 1, [][]string{{"e", "ev_b"}}))

	g.Resolve()

	s := g.Stats()
	if s.RefBonds != 2 {
		t.Fatalf("expected 2 reference bonds in reply chain, got %d", s.RefBonds)
	}
	if s.Orphans != 0 {
		t.Fatalf("expected 0 orphans in fully resolved chain, got %d", s.Orphans)
	}
}
