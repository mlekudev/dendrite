package profile

import (
	"sync"

	"github.com/mlekudev/dendrite/pkg/grow"
)

// Collector accumulates a Profile from growth events.
// Thread-safe: multiple goroutines can call Record concurrently.
type Collector struct {
	mu      sync.Mutex
	profile *Profile
	lastTag string // tag of most recently bonded element, for transition tracking
}

// NewCollector creates a collector with an empty profile.
func NewCollector() *Collector {
	return &Collector{profile: New()}
}

// RecordGrowEvent processes a single growth event.
func (c *Collector) RecordGrowEvent(ev grow.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.profile.TokensIngested++

	switch ev.Type {
	case grow.EventBonded:
		c.profile.BondEvents++
		c.profile.PathFreq[ev.NodeID]++
		c.profile.WalkDistHist[ev.Steps]++
		if ev.Element != nil {
			tag := ev.Element.Type()
			c.profile.BondDist[tag]++
			if c.lastTag != "" {
				c.profile.TransitionFreq[[2]string{c.lastTag, tag}]++
			}
			c.lastTag = tag
		}
	case grow.EventRejected:
		c.profile.RejectEvents++
	case grow.EventExpired:
		c.profile.ExpireEvents++
	}
}

// RecordProbeEvent processes a single probe event from inference on a
// trained lattice. Probe matches record into the same profile structure
// as growth bonds — the statistical fingerprint is what matters, not
// whether bonding actually occurred.
func (c *Collector) RecordProbeEvent(ev grow.ProbeEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.profile.TokensIngested++

	switch ev.Type {
	case grow.EventBonded:
		c.profile.BondEvents++
		c.profile.PathFreq[ev.NodeID]++
		c.profile.WalkDistHist[ev.Steps]++
		if ev.Element != nil {
			tag := ev.Element.Type()
			c.profile.BondDist[tag]++
			if c.lastTag != "" {
				c.profile.TransitionFreq[[2]string{c.lastTag, tag}]++
			}
			c.lastTag = tag
		}
	case grow.EventExpired:
		c.profile.ExpireEvents++
	}
}

// RecordNewVertex increments the new vertex counter.
// Called when the hexagram engine creates a new node (OpNucleate/OpExplore).
func (c *Collector) RecordNewVertex() {
	c.mu.Lock()
	c.profile.NewVertices++
	c.mu.Unlock()
}

// Snapshot returns a deep copy of the current profile.
func (c *Collector) Snapshot() *Profile {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.profile.Clone()
}
