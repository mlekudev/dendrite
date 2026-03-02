// Package mindsicle implements the frozen lattice — the full graph state
// serialized for persistence and bootstrapping.
//
// A mindsicle is a lossless snapshot of the lattice: every node, edge,
// occupant, candidate, constraint tag, hex state, permutation, projection,
// and age. The spore compresses this to statistics; the mindsicle preserves
// the actual crystal.
//
// Freeze captures a live lattice into a Mindsicle. Thaw reconstitutes
// the lattice from the frozen state. The bootstrapper reads a mindsicle,
// thaws it, emits Go source, compiles, and runs the result.
package mindsicle

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
	"github.com/mlekudev/dendrite/pkg/ratio"
	"github.com/mlekudev/dendrite/pkg/spore"
	"github.com/mlekudev/dendrite/pkg/state"
)

// Version is the current mindsicle format version.
const Version = 1

// Mindsicle is a frozen lattice — the complete graph state serialized.
type Mindsicle struct {
	Spore    *spore.Spore `json:"spore"`
	Nodes    []NodeRecord `json:"nodes"`
	Version  int          `json:"version"`
	FrozenAt time.Time    `json:"frozen_at"`
}

// NodeRecord is the serialized form of a single lattice node.
type NodeRecord struct {
	ID         uint64          `json:"id"`
	Tags       []string        `json:"constraints"`
	Occupant   *ElementRecord  `json:"occupant,omitempty"`
	Candidates []ElementRecord `json:"candidates,omitempty"`
	Neighbors  []uint64        `json:"neighbors"`
	Hex        uint8           `json:"hex"`
	LockIn     ratio.Ratio     `json:"lock_in"`
	BondCount  int             `json:"bond_count"`
	Perm       uint8           `json:"perm"`
	ProjVertex uint8           `json:"proj_vertex"`
	ProjKey    uint8           `json:"proj_key"`
	ProjPath   uint16          `json:"proj_path"`
	Age        uint8           `json:"age"`
}

// ElementRecord is the serialized form of an element.
type ElementRecord struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// frozenElement implements axiom.Element for deserialized elements.
type frozenElement struct {
	typ string
	val string
}

func (e frozenElement) Type() string { return e.typ }
func (e frozenElement) Value() any   { return e.val }

// Freeze captures a live lattice into a Mindsicle.
// If sp is nil, no spore is stored (Thaw doesn't need it).
func Freeze(l *lattice.Lattice, sp *spore.Spore) *Mindsicle {
	nodes := l.Nodes()
	records := make([]NodeRecord, len(nodes))

	for i, n := range nodes {
		rec := NodeRecord{
			ID:         uint64(n.ID()),
			Hex:        uint8(n.Hexagram()),
			LockIn:     n.LockIn(),
			BondCount:  n.BondCount(),
			Perm:       n.Permutation(),
			ProjVertex: n.ProjectionVertex(),
			ProjKey:    n.ProjectionKey(),
			ProjPath:   n.ProjectionPath(),
			Age:        n.Age(),
		}

		// Constraint tags.
		for _, c := range n.Constraints() {
			rec.Tags = append(rec.Tags, c.Tag())
		}

		// Occupant.
		if n.Occupied() {
			e := n.Occupant()
			val := ""
			if e.Value() != nil {
				val = fmt.Sprintf("%v", e.Value())
			}
			rec.Occupant = &ElementRecord{
				Type:  e.Type(),
				Value: val,
			}
		}

		// Candidates.
		for _, c := range n.Candidates() {
			val := ""
			if c.Value() != nil {
				val = fmt.Sprintf("%v", c.Value())
			}
			rec.Candidates = append(rec.Candidates, ElementRecord{
				Type:  c.Type(),
				Value: val,
			})
		}

		// Neighbors (as IDs).
		for _, nb := range n.Neighbors() {
			rec.Neighbors = append(rec.Neighbors, uint64(nb.ID()))
		}

		records[i] = rec
	}

	return &Mindsicle{
		Spore:    sp,
		Nodes:    records,
		Version:  Version,
		FrozenAt: time.Now(),
	}
}

// Thaw reconstitutes a live lattice from a frozen Mindsicle.
//
// constraintFactory converts tag strings back into Constraint instances.
// The thaw proceeds in four phases:
//  1. Create nodes with constraints
//  2. Connect neighbors (dedup bidirectional pairs)
//  3. ForceOccupant + candidates
//  4. Restore age, permutation, projection, hex energy/outer
func (m *Mindsicle) Thaw(constraintFactory func(string) axiom.Constraint) *lattice.Lattice {
	l := lattice.New()

	if len(m.Nodes) == 0 {
		return l
	}

	// Phase 1: Create all nodes with their constraints.
	nodeByID := make(map[uint64]*lattice.Node, len(m.Nodes))
	for _, rec := range m.Nodes {
		constraints := make([]axiom.Constraint, len(rec.Tags))
		for j, tag := range rec.Tags {
			constraints[j] = constraintFactory(tag)
		}
		n := l.AddNode(constraints)
		nodeByID[uint64(n.ID())] = n
	}

	// Phase 2: Connect neighbors. Track connected pairs to avoid
	// double-connecting (edges are stored bidirectionally in records).
	type edge struct{ a, b uint64 }
	connected := make(map[edge]bool)
	for _, rec := range m.Nodes {
		for _, nbID := range rec.Neighbors {
			a, b := rec.ID, nbID
			if a > b {
				a, b = b, a
			}
			e := edge{a, b}
			if connected[e] {
				continue
			}
			connected[e] = true
			na, nb := nodeByID[a], nodeByID[b]
			if na != nil && nb != nil {
				l.Connect(na, nb)
			}
		}
	}

	// Phase 3: Place occupants and candidates. Uses ForceOccupant to
	// bypass constraint checking — the frozen state is pre-validated.
	for _, rec := range m.Nodes {
		n := nodeByID[rec.ID]
		if n == nil {
			continue
		}

		if rec.Occupant != nil {
			elem := frozenElement{typ: rec.Occupant.Type, val: rec.Occupant.Value}
			n.ForceOccupant(elem, rec.BondCount)
		}

		for _, c := range rec.Candidates {
			elem := frozenElement{typ: c.Type, val: c.Value}
			n.AddCandidate(elem)
		}
	}

	// Phase 4: Restore age, permutation, projection, hex state.
	for _, rec := range m.Nodes {
		n := nodeByID[rec.ID]
		if n == nil {
			continue
		}

		n.RestoreAge(rec.Age)
		n.SetPermutation(rec.Perm)
		n.SetProjection(rec.ProjVertex, rec.ProjKey, rec.ProjPath)

		// Restore energy bit and outer trigram from the frozen hexagram.
		hex := state.Hexagram(rec.Hex)
		n.SetEnergy(hex.Inner().Energy())
		n.SetOuterTrigram(hex.Outer())
	}

	return l
}

// WriteTo serializes the mindsicle to JSON.
func (m *Mindsicle) WriteTo(w io.Writer) (int64, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return 0, err
	}
	n, err := w.Write(data)
	return int64(n), err
}

// ReadMindsicle deserializes a mindsicle from JSON.
func ReadMindsicle(r io.Reader) (*Mindsicle, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var m Mindsicle
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
