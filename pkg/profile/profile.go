// Package profile records and analyzes walk/bond statistics during lattice
// growth. The statistical signature of how text bonds into a lattice is the
// detection mechanism: human text and AI text produce measurably different
// profiles on a human-trained lattice.
//
// All derived statistics use exact rational arithmetic (ratio.Ratio) to
// guarantee deterministic, platform-independent results.
package profile

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mlekudev/dendrite/pkg/lattice"
)

// Profile captures raw statistics from a growth session.
type Profile struct {
	// Counters.
	TokensIngested int64 `json:"tokens_ingested"`
	BondEvents     int64 `json:"bond_events"`
	RejectEvents   int64 `json:"reject_events"`
	ExpireEvents   int64 `json:"expire_events"`
	NewVertices    int64 `json:"new_vertices"`

	// PathFreq counts how many times each node was the bonding site.
	PathFreq map[lattice.NodeID]int64 `json:"path_freq"`

	// BondDist counts bonds per constraint tag.
	BondDist map[string]int64 `json:"bond_dist"`

	// WalkDistHist is a histogram of walk steps before bonding.
	// Key is the step count, value is how many bonds occurred at that distance.
	WalkDistHist map[int]int64 `json:"walk_dist_hist"`

	// TransitionFreq counts (previous_element_tag, bonded_element_tag) pairs.
	// Captures sequential structure without prescribing grammar.
	// Not directly JSON-serializable; use Marshal/Unmarshal methods.
	TransitionFreq map[[2]string]int64 `json:"-"`
}

// New creates a zero-valued Profile with initialized maps.
func New() *Profile {
	return &Profile{
		PathFreq:       make(map[lattice.NodeID]int64),
		BondDist:       make(map[string]int64),
		WalkDistHist:   make(map[int]int64),
		TransitionFreq: make(map[[2]string]int64),
	}
}

// Clone returns a deep copy of the profile.
func (p *Profile) Clone() *Profile {
	c := &Profile{
		TokensIngested: p.TokensIngested,
		BondEvents:     p.BondEvents,
		RejectEvents:   p.RejectEvents,
		ExpireEvents:   p.ExpireEvents,
		NewVertices:    p.NewVertices,
		PathFreq:       make(map[lattice.NodeID]int64, len(p.PathFreq)),
		BondDist:       make(map[string]int64, len(p.BondDist)),
		WalkDistHist:   make(map[int]int64, len(p.WalkDistHist)),
		TransitionFreq: make(map[[2]string]int64, len(p.TransitionFreq)),
	}
	for k, v := range p.PathFreq {
		c.PathFreq[k] = v
	}
	for k, v := range p.BondDist {
		c.BondDist[k] = v
	}
	for k, v := range p.WalkDistHist {
		c.WalkDistHist[k] = v
	}
	for k, v := range p.TransitionFreq {
		c.TransitionFreq[k] = v
	}
	return c
}

// profileJSON is the JSON-serializable form of Profile.
// TransitionFreq keys are encoded as "tag1\x00tag2" strings.
type profileJSON struct {
	TokensIngested int64                    `json:"tokens_ingested"`
	BondEvents     int64                    `json:"bond_events"`
	RejectEvents   int64                    `json:"reject_events"`
	ExpireEvents   int64                    `json:"expire_events"`
	NewVertices    int64                    `json:"new_vertices"`
	PathFreq       map[lattice.NodeID]int64 `json:"path_freq"`
	BondDist       map[string]int64         `json:"bond_dist"`
	WalkDistHist   map[int]int64            `json:"walk_dist_hist"`
	TransitionFreq map[string]int64         `json:"transition_freq"`
}

// transitionKey encodes a [2]string as a single string for JSON map keys.
func transitionKey(pair [2]string) string {
	return fmt.Sprintf("%s\x00%s", pair[0], pair[1])
}

// parseTransitionKey decodes a transition key back to [2]string.
func parseTransitionKey(key string) [2]string {
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) == 2 {
		return [2]string{parts[0], parts[1]}
	}
	return [2]string{key, ""}
}

// Marshal serializes the profile to JSON.
func (p *Profile) Marshal() ([]byte, error) {
	j := profileJSON{
		TokensIngested: p.TokensIngested,
		BondEvents:     p.BondEvents,
		RejectEvents:   p.RejectEvents,
		ExpireEvents:   p.ExpireEvents,
		NewVertices:    p.NewVertices,
		PathFreq:       p.PathFreq,
		BondDist:       p.BondDist,
		WalkDistHist:   p.WalkDistHist,
		TransitionFreq: make(map[string]int64, len(p.TransitionFreq)),
	}
	for k, v := range p.TransitionFreq {
		j.TransitionFreq[transitionKey(k)] = v
	}
	return json.Marshal(j)
}

// Unmarshal deserializes a profile from JSON.
func Unmarshal(data []byte) (*Profile, error) {
	var j profileJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	p := &Profile{
		TokensIngested: j.TokensIngested,
		BondEvents:     j.BondEvents,
		RejectEvents:   j.RejectEvents,
		ExpireEvents:   j.ExpireEvents,
		NewVertices:    j.NewVertices,
		PathFreq:       j.PathFreq,
		BondDist:       j.BondDist,
		WalkDistHist:   j.WalkDistHist,
		TransitionFreq: make(map[[2]string]int64, len(j.TransitionFreq)),
	}
	if p.PathFreq == nil {
		p.PathFreq = make(map[lattice.NodeID]int64)
	}
	if p.BondDist == nil {
		p.BondDist = make(map[string]int64)
	}
	if p.WalkDistHist == nil {
		p.WalkDistHist = make(map[int]int64)
	}
	for k, v := range j.TransitionFreq {
		p.TransitionFreq[parseTransitionKey(k)] = v
	}
	return p, nil
}
