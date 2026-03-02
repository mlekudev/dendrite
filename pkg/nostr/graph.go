package nostr

import "strconv"

// EventGraph builds a coherence graph from ingested Nostr events.
// Events cluster by author (pubkey) and kind. References (e-tags, p-tags)
// form bonds between events. References to unknown events or pubkeys
// are recorded as orphans — the negative space of what's missing.
//
// This is Stage 4b (Coagula): evaluate what was ingested, find the
// structure, and identify the gaps.

// GraphNode is one event in the graph, with its cluster memberships
// and reference edges.
type GraphNode struct {
	Event  *Event
	InRefs []string // IDs of events that reference this one
}

// Cluster groups events that share a common property.
type Cluster struct {
	Key    string   // the shared value (pubkey hex, or "kind:<N>")
	Type   string   // "author" or "kind"
	Events []string // event IDs in this cluster
}

// Orphan is a reference to something not present in the graph.
// It is the shape of what's missing — typed negative space.
type Orphan struct {
	Type   string // "event" or "pubkey"
	ID     string // the missing event ID or pubkey hex
	RefBy  string // the event ID that references it
	TagIdx int    // index of the tag within the referencing event
}

// GraphStats summarizes the graph structure.
type GraphStats struct {
	TotalEvents    int
	AuthorClusters int // distinct pubkeys
	KindClusters   int // distinct kinds
	RefBonds       int // resolved e-tag references (both ends present)
	Orphans        int // unresolved references
	LargestAuthor  int // size of the largest author cluster
	LargestKind    int // size of the largest kind cluster
}

// EventGraph is the coherence graph over a set of ingested events.
type EventGraph struct {
	Nodes    map[string]*GraphNode // event ID → node
	Authors  map[string]*Cluster   // pubkey → author cluster
	Kinds    map[string]*Cluster   // "kind:<N>" → kind cluster
	Orphans  []Orphan              // unresolved references
	PubkeysR map[string][]string   // pubkey → event IDs that p-tag it (for resolve check)
}

// NewEventGraph creates an empty event graph.
func NewEventGraph() *EventGraph {
	return &EventGraph{
		Nodes:    make(map[string]*GraphNode),
		Authors:  make(map[string]*Cluster),
		Kinds:    make(map[string]*Cluster),
		PubkeysR: make(map[string][]string),
	}
}

// Add inserts a validated event into the graph.
// The event is added to its author and kind clusters.
func (g *EventGraph) Add(ev *Event) {
	if _, exists := g.Nodes[ev.ID]; exists {
		return // deduplicate
	}

	g.Nodes[ev.ID] = &GraphNode{Event: ev}

	// Author cluster.
	ac, ok := g.Authors[ev.Pubkey]
	if !ok {
		ac = &Cluster{Key: ev.Pubkey, Type: "author"}
		g.Authors[ev.Pubkey] = ac
	}
	ac.Events = append(ac.Events, ev.ID)

	// Kind cluster.
	kk := kindKey(ev.Kind)
	kc, ok := g.Kinds[kk]
	if !ok {
		kc = &Cluster{Key: kk, Type: "kind"}
		g.Kinds[kk] = kc
	}
	kc.Events = append(kc.Events, ev.ID)
}

// Resolve walks all events, resolving e-tag and p-tag references.
// Resolved references become InRef edges on the target node.
// Unresolved references become Orphans.
func (g *EventGraph) Resolve() {
	g.Orphans = nil

	for _, node := range g.Nodes {
		for i, tag := range node.Event.Tags {
			if len(tag) < 2 {
				continue
			}
			switch tag[0] {
			case "e":
				// Event reference.
				targetID := tag[1]
				if target, ok := g.Nodes[targetID]; ok {
					// Bond: both ends present.
					target.InRefs = append(target.InRefs, node.Event.ID)
				} else {
					// Orphan: referencing an event we don't have.
					g.Orphans = append(g.Orphans, Orphan{
						Type:   "event",
						ID:     targetID,
						RefBy:  node.Event.ID,
						TagIdx: i,
					})
				}
			case "p":
				// Pubkey mention.
				targetPK := tag[1]
				g.PubkeysR[targetPK] = append(g.PubkeysR[targetPK], node.Event.ID)
				if _, ok := g.Authors[targetPK]; !ok {
					// Orphan: mentioning an author we haven't seen.
					g.Orphans = append(g.Orphans, Orphan{
						Type:   "pubkey",
						ID:     targetPK,
						RefBy:  node.Event.ID,
						TagIdx: i,
					})
				}
			}
		}
	}
}

// Stats returns a summary of the graph structure.
func (g *EventGraph) Stats() GraphStats {
	s := GraphStats{
		TotalEvents:    len(g.Nodes),
		AuthorClusters: len(g.Authors),
		KindClusters:   len(g.Kinds),
		Orphans:        len(g.Orphans),
	}

	// Count resolved reference bonds.
	for _, node := range g.Nodes {
		s.RefBonds += len(node.InRefs)
	}

	// Find largest clusters.
	for _, c := range g.Authors {
		if len(c.Events) > s.LargestAuthor {
			s.LargestAuthor = len(c.Events)
		}
	}
	for _, c := range g.Kinds {
		if len(c.Events) > s.LargestKind {
			s.LargestKind = len(c.Events)
		}
	}

	return s
}

// OrphansByType returns orphans grouped by type ("event" or "pubkey").
func (g *EventGraph) OrphansByType() map[string][]Orphan {
	m := make(map[string][]Orphan)
	for _, o := range g.Orphans {
		m[o.Type] = append(m[o.Type], o)
	}
	return m
}

// kindKey produces the cluster key for a kind value.
func kindKey(kind int) string {
	return "kind:" + strconv.Itoa(kind)
}
