package cayley

import (
	"encoding/json"
	"io"
	"time"

	"github.com/mlekudev/dendrite/pkg/hadamard"
)

// Snapshot is a frozen Cayley tree — the serializable form.
// Replaces the old mindsicle format.
type Snapshot struct {
	Version       int                      `json:"version"`
	FrozenAt      time.Time                `json:"frozen_at"`
	Levels        [MaxDepth]LevelRecord    `json:"levels"`
	OriginProfile hadamard.Vec8            `json:"origin_profile"`
	TokenCount    int64                    `json:"token_count"`
}

// LevelRecord is the serializable form of a Level.
type LevelRecord struct {
	Spatial       hadamard.Vec8 `json:"spatial"`
	Walsh         hadamard.Vec8 `json:"walsh"`
	BondCount     hadamard.Vec8 `json:"bond_count"`
	MissCount     hadamard.Vec8 `json:"miss_count"`
	TotalWalkDist hadamard.Vec8 `json:"total_walk_dist"`
}

// Freeze creates a snapshot from a live tree.
func Freeze(t *Tree) *Snapshot {
	s := &Snapshot{
		Version:       2,
		FrozenAt:      time.Now(),
		OriginProfile: t.OriginProfile,
		TokenCount:    t.TokenCount,
	}
	for i := range t.Levels {
		t.Levels[i].mu.RLock()
		s.Levels[i] = LevelRecord{
			Spatial:       t.Levels[i].Spatial,
			Walsh:         t.Levels[i].Walsh,
			BondCount:     t.Levels[i].BondCount,
			MissCount:     t.Levels[i].MissCount,
			TotalWalkDist: t.Levels[i].TotalWalkDist,
		}
		t.Levels[i].mu.RUnlock()
	}
	return s
}

// Thaw reconstructs a live tree from a snapshot.
func Thaw(s *Snapshot) *Tree {
	t := NewTree()
	t.OriginProfile = s.OriginProfile
	t.TokenCount = s.TokenCount
	for i := range s.Levels {
		t.Levels[i].Spatial = s.Levels[i].Spatial
		t.Levels[i].Walsh = s.Levels[i].Walsh
		t.Levels[i].BondCount = s.Levels[i].BondCount
		t.Levels[i].MissCount = s.Levels[i].MissCount
		t.Levels[i].TotalWalkDist = s.Levels[i].TotalWalkDist
	}
	return t
}

// MarshalJSON serializes the snapshot.
func (s *Snapshot) MarshalJSON() ([]byte, error) {
	// Use an alias to avoid infinite recursion.
	type alias Snapshot
	return json.Marshal((*alias)(s))
}

// WriteTo writes the snapshot as JSON to w.
func (s *Snapshot) WriteTo(w io.Writer) (int64, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return 0, err
	}
	n, err := w.Write(data)
	return int64(n), err
}

// ReadSnapshot deserializes a snapshot from r.
func ReadSnapshot(r io.Reader) (*Snapshot, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	return &s, json.Unmarshal(data, &s)
}
