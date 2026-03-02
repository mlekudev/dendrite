// Package detect provides a reusable 8-pass lattice detection chain.
// It loads trained mindsicles, thaws them into read-only lattices,
// and runs the full SeqProbe cascade on arbitrary text input.
//
// Thread-safe: Detect can be called concurrently from multiple goroutines.
package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/enzyme"
	"github.com/mlekudev/dendrite/pkg/grammar"
	"github.com/mlekudev/dendrite/pkg/grow"
	"github.com/mlekudev/dendrite/pkg/lattice"
	"github.com/mlekudev/dendrite/pkg/memory"
	"github.com/mlekudev/dendrite/pkg/mindsicle"
)

// Detector holds thawed lattices for the multi-pass detection chain.
// Lattices are read-only after thaw; Detect is safe for concurrent use.
type Detector struct {
	lattices      []*lattice.Lattice
	trollLattices []*lattice.Lattice
	passes        int
	trollPasses   int
	window        int
	probeCfg      grow.Config
}

// NewDetector loads trained mindsicles from the badger DB, thaws them
// into live lattices, and closes the DB. The returned Detector holds
// only in-memory lattice graphs.
func NewDetector(memoryDir string, passes, window int) (*Detector, error) {
	db, err := memory.Open(memoryDir)
	if err != nil {
		return nil, fmt.Errorf("open memory: %w", err)
	}
	defer db.Close()

	d := &Detector{
		lattices: make([]*lattice.Lattice, passes),
		passes:   passes,
		window:   window,
		probeCfg: grow.Config{MaxSteps: 3, Workers: 1},
	}

	// Thaw gen 1: word lattice.
	wordData, err := db.LoadMindsicle(1)
	if err != nil {
		return nil, fmt.Errorf("load word mindsicle: %w", err)
	}
	var wm mindsicle.Mindsicle
	if err := json.Unmarshal(wordData, &wm); err != nil {
		return nil, fmt.Errorf("unmarshal word mindsicle: %w", err)
	}
	d.lattices[0] = wm.Thaw(func(tag string) axiom.Constraint {
		return grammar.NewConstraint(tag, grammar.NaturalText)
	})

	// Thaw gen 2..N: event lattices.
	for p := 1; p < passes; p++ {
		data, err := db.LoadMindsicle(uint32(p + 1))
		if err != nil {
			return nil, fmt.Errorf("load event mindsicle gen %d: %w", p+1, err)
		}
		var em mindsicle.Mindsicle
		if err := json.Unmarshal(data, &em); err != nil {
			return nil, fmt.Errorf("unmarshal event mindsicle gen %d: %w", p+1, err)
		}
		d.lattices[p] = em.Thaw(func(tag string) axiom.Constraint {
			return grammar.NewConstraint(tag, grammar.BondEvent)
		})
	}

	return d, nil
}

// LoadTrollLattices loads a second set of mindsicles trained on manipulation text.
func (d *Detector) LoadTrollLattices(memoryDir string, passes int) error {
	db, err := memory.Open(memoryDir)
	if err != nil {
		return fmt.Errorf("open troll memory: %w", err)
	}
	defer db.Close()

	d.trollLattices = make([]*lattice.Lattice, passes)
	d.trollPasses = passes

	wordData, err := db.LoadMindsicle(1)
	if err != nil {
		return fmt.Errorf("load troll word mindsicle: %w", err)
	}
	var wm mindsicle.Mindsicle
	if err := json.Unmarshal(wordData, &wm); err != nil {
		return fmt.Errorf("unmarshal troll word mindsicle: %w", err)
	}
	d.trollLattices[0] = wm.Thaw(func(tag string) axiom.Constraint {
		return grammar.NewConstraint(tag, grammar.NaturalText)
	})

	for p := 1; p < passes; p++ {
		data, err := db.LoadMindsicle(uint32(p + 1))
		if err != nil {
			return fmt.Errorf("load troll event mindsicle gen %d: %w", p+1, err)
		}
		var em mindsicle.Mindsicle
		if err := json.Unmarshal(data, &em); err != nil {
			return fmt.Errorf("unmarshal troll event mindsicle gen %d: %w", p+1, err)
		}
		d.trollLattices[p] = em.Thaw(func(tag string) axiom.Constraint {
			return grammar.NewConstraint(tag, grammar.BondEvent)
		})
	}

	return nil
}

// Detect runs the multi-pass SeqProbe chain on text content and returns a verdict.
func (d *Detector) Detect(ctx context.Context, content string) grammar.Verdict {
	solution := enzyme.Text{}.Digest(strings.NewReader(content))
	solution = limitTokens(solution, d.window)

	var tokens []axiom.Element
	for tok := range solution {
		tokens = append(tokens, tok)
	}

	aiStats := d.runChain(ctx, d.lattices, d.passes, tokens)
	verdict := grammar.Score(aiStats)

	if len(d.trollLattices) > 0 {
		trollStats := d.runChain(ctx, d.trollLattices, d.trollPasses, tokens)
		grammar.ScoreTroll(&verdict, trollStats)
	}

	return verdict
}

func (d *Detector) runChain(ctx context.Context, lattices []*lattice.Lattice, passes int, tokens []axiom.Element) []grammar.PassStats {
	tokenCh := make(chan axiom.Element, len(tokens))
	for _, tok := range tokens {
		tokenCh <- tok
	}
	close(tokenCh)

	eventsCh := make(chan grow.Event, 256)
	var prevEvents []grow.Event
	done := make(chan struct{})
	go func() {
		for ev := range eventsCh {
			prevEvents = append(prevEvents, ev)
		}
		close(done)
	}()
	grow.SeqProbe(ctx, lattices[0], tokenCh, d.probeCfg, eventsCh)
	close(eventsCh)
	<-done

	allPassStats := make([]grammar.PassStats, passes)
	b, e, t := countEvents(prevEvents)
	allPassStats[0] = grammar.PassStats{Events: prevEvents, Bonded: b, Expired: e, Total: t}

	for p := 1; p < passes; p++ {
		classified := classifyEvents(prevEvents)
		nextEvents := seqProbeElements(ctx, lattices[p], classified, d.probeCfg)
		b, e, t = countEvents(nextEvents)
		allPassStats[p] = grammar.PassStats{Events: nextEvents, Bonded: b, Expired: e, Total: t}
		prevEvents = nextEvents
	}

	return allPassStats
}

func classifyEvents(events []grow.Event) []axiom.Element {
	elems := make([]axiom.Element, len(events))
	for i, ev := range events {
		elems[i] = grammar.ClassifyEvent(ev)
	}
	return elems
}

func seqProbeElements(ctx context.Context, l *lattice.Lattice, elems []axiom.Element, cfg grow.Config) []grow.Event {
	ch := make(chan axiom.Element, 256)
	go func() {
		defer close(ch)
		for _, e := range elems {
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()

	eventsCh := make(chan grow.Event, 256)
	var events []grow.Event
	done := make(chan struct{})
	go func() {
		for ev := range eventsCh {
			events = append(events, ev)
		}
		close(done)
	}()

	grow.SeqProbe(ctx, l, ch, cfg, eventsCh)
	close(eventsCh)
	<-done

	return events
}

func countEvents(events []grow.Event) (bonded, expired, total int64) {
	for _, ev := range events {
		switch ev.Type {
		case grow.EventBonded:
			bonded++
		case grow.EventExpired:
			expired++
		}
		total++
	}
	return
}

func limitTokens(in <-chan axiom.Element, max int) <-chan axiom.Element {
	if max <= 0 {
		return in
	}
	out := make(chan axiom.Element, cap(in))
	go func() {
		defer close(out)
		n := 0
		for e := range in {
			if n >= max {
				for range in {
				}
				return
			}
			out <- e
			n++
		}
	}()
	return out
}
