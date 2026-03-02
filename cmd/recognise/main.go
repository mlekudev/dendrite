// Command recognise builds and queries text recognition lattices.
//
// Modes:
//
//	recognise -train  -corpus ./texts -memory ./recog_db
//	recognise -detect -memory ./recog_db -sample ./input.txt
//	recognise -fingerprint -model chatgpt -corpus ./chatgpt_texts -memory ./recog_db
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/converge"
	"github.com/mlekudev/dendrite/pkg/enzyme"
	"github.com/mlekudev/dendrite/pkg/extract"
	"github.com/mlekudev/dendrite/pkg/grammar"
	"github.com/mlekudev/dendrite/pkg/grow"
	"github.com/mlekudev/dendrite/pkg/lattice"
	"github.com/mlekudev/dendrite/pkg/memory"
	"github.com/mlekudev/dendrite/pkg/mindsicle"
	"github.com/mlekudev/dendrite/pkg/profile"
)

func main() {
	var (
		train       = flag.Bool("train", false, "train baseline lattice from corpus directory")
		detect      = flag.Bool("detect", false, "detect AI text in a sample")
		fingerprint = flag.Bool("fingerprint", false, "build model fingerprint from labeled corpus")

		corpusDir   = flag.String("corpus", "", "corpus directory (for -train and -fingerprint)")
		memoryDir   = flag.String("memory", ".recognise_db", "persistent memory directory")
		trollMemory = flag.String("troll-memory", "", "badger DB with manipulation-trained mindsicles (for -detect)")
		trollPassN  = flag.Int("troll-passes", 8, "number of troll detection passes")
		sampleFile  = flag.String("sample", "", "text file to analyze (for -detect)")
		modelName   = flag.String("model", "", "model name (for -fingerprint)")
		latticeN    = flag.Int("nodes", 4096, "number of lattice nodes")
		maxSteps    = flag.Int("max-steps", 500, "max walk steps per element")
		passes      = flag.Int("passes", 4, "number of recognition passes (1=word only, 2+=event levels)")
		window      = flag.Int("window", 500, "max tokens to process per sample (0=unlimited)")
	)

	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	modeCount := 0
	if *train {
		modeCount++
	}
	if *detect {
		modeCount++
	}
	if *fingerprint {
		modeCount++
	}
	if modeCount != 1 {
		fmt.Fprintln(os.Stderr, "exactly one of -train, -detect, -fingerprint required")
		flag.Usage()
		os.Exit(1)
	}

	switch {
	case *train:
		if *corpusDir == "" {
			log.Fatal("-corpus required for -train mode")
		}
		if err := runTrain(ctx, *corpusDir, *memoryDir, *latticeN, *maxSteps, *passes); err != nil {
			log.Fatal(err)
		}

	case *detect:
		if *sampleFile == "" {
			log.Fatal("-sample required for -detect mode")
		}
		if err := runDetect(ctx, *sampleFile, *memoryDir, trollMemory, *latticeN, *maxSteps, *passes, *trollPassN, *window); err != nil {
			log.Fatal(err)
		}

	case *fingerprint:
		if *corpusDir == "" || *modelName == "" {
			log.Fatal("-corpus and -model required for -fingerprint mode")
		}
		if err := runFingerprint(ctx, *corpusDir, *memoryDir, *modelName, *latticeN, *maxSteps); err != nil {
			log.Fatal(err)
		}
	}
}

// runTrain builds an N-pass recognition system from a human text corpus.
//
// Pass 1: Train a word-level lattice from the corpus until convergence.
// Pass 2..N: Each subsequent pass takes the bond events from the previous
// pass, classifies them (snap/near/far/miss), and grows them into a new
// event-level lattice. Each level captures progressively higher-order
// structural patterns — the rhythm of rhythms.
//
// All lattices are saved as mindsicles (gen 1..N) for detection.
func runTrain(ctx context.Context, corpusDir, memoryDir string, latticeSize, maxSteps, numPasses int) error {
	if numPasses < 1 {
		numPasses = 1
	}

	db, err := memory.Open(memoryDir)
	if err != nil {
		return fmt.Errorf("open memory: %w", err)
	}
	defer db.Close()

	l, seed := buildTextLattice(latticeSize)
	tracker := converge.NewTracker(converge.DefaultWindowSize, converge.DefaultThreshold)

	cfg := grow.Config{
		MaxSteps: maxSteps,
		Workers:  grow.WorkerCount(),
	}

	registry := extract.NewRegistry()
	files, err := collectFiles(corpusDir)
	if err != nil {
		return fmt.Errorf("walk corpus: %w", err)
	}

	fmt.Printf("=== pass 1/%d: word-level lattice ===\n", numPasses)
	fmt.Printf("training on %d files from %s\n", len(files), corpusDir)
	fmt.Printf("lattice: %d nodes, seed: %x...\n", l.Size(), seed[:4])

	// Pass 1: Grow word lattice until convergence.
	convergedAt := len(files)
	for i, path := range files {
		select {
		case <-ctx.Done():
			fmt.Println("\ninterrupted, saving progress...")
			convergedAt = i
			goto chain
		default:
		}

		if err := ingestFileSimple(ctx, l, path, registry, cfg, tracker); err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", path, err)
			continue
		}

		if (i+1)%100 == 0 || i == len(files)-1 {
			h := l.Health()
			fmt.Printf("  [%d/%d] occupied=%d/%d convergence_rate=%s\n",
				i+1, len(files), h.Occupied, h.NodeCount,
				tracker.Report().CurrentRate.String())
		}

		if tracker.IsConverged() {
			convergedAt = i + 1
			fmt.Printf("  converged at file %d/%d\n", convergedAt, len(files))
			break
		}
	}

chain:
	// Save word lattice as gen 1.
	if err := saveLattice(db, l, 1); err != nil {
		return err
	}

	h := l.Health()
	fmt.Printf("word lattice: %d/%d occupied\n", h.Occupied, h.NodeCount)

	// Validation files for event-level passes.
	// Cap validation to 10 files — more doesn't help and takes forever.
	validationFiles := files[convergedAt:]
	if len(validationFiles) == 0 {
		n := len(files)
		if n > 5 {
			n = 5
		}
		validationFiles = files[:n]
	}
	if len(validationFiles) > 10 {
		validationFiles = validationFiles[:10]
	}

	if numPasses < 2 {
		return nil
	}

	// Build the chain of event lattices (passes 2..N).
	// Each pass: clear previous lattice, run validation through it,
	// collect events, classify, grow into next event lattice.
	eventLattices := make([]*lattice.Lattice, numPasses-1)
	for i := range eventLattices {
		// Each successive event lattice is smaller — fewer patterns at higher levels.
		sz := latticeSize / (2 * (i + 1))
		if sz < 64 {
			sz = 64
		}
		eventLattices[i] = buildEventLattice(sz)
	}

	// Run the chain. For each pass p (2..N):
	//   - Source lattice = pass p-1's lattice (cleared)
	//   - Target lattice = pass p's event lattice
	//   - Input = validation files (pass 2) or previous events (pass 3+)

	// First, collect word-level events from validation files.
	l.ClearOccupants()

	fmt.Printf("\n=== pass 2/%d: event-level lattice ===\n", numPasses)
	fmt.Printf("validation files: %d\n", len(validationFiles))

	// Collect all word-level events from validation files.
	var allWordEvents []grow.Event
	for _, path := range validationFiles {
		select {
		case <-ctx.Done():
			goto done
		default:
		}
		evs, err := collectFileEvents(ctx, l, path, registry, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", path, err)
			continue
		}
		allWordEvents = append(allWordEvents, evs...)
	}

	// Now chain through passes 2..N.
	{
		prevEvents := allWordEvents
		for p := 0; p < len(eventLattices); p++ {
			passNum := p + 2
			el := eventLattices[p]

			// Classify previous events and grow into this event lattice.
			classified := classifyEvents(prevEvents)
			nextEvents := growElements(ctx, el, classified, cfg, false)

			bonded, expired, total := countEvents(nextEvents)
			fmt.Printf("  pass %d/%d: bonded=%d expired=%d total=%d rate=%.2f%%\n",
				passNum, numPasses, bonded, expired, total, pct(bonded, total))

			// Save this event lattice.
			if err := saveLattice(db, el, uint32(passNum)); err != nil {
				return err
			}

			eh := el.Health()
			fmt.Printf("  event lattice %d: %d/%d occupied\n", passNum, eh.Occupied, eh.NodeCount)

			if p < len(eventLattices)-1 {
				fmt.Printf("\n=== pass %d/%d: meta-event lattice ===\n", passNum+1, numPasses)
				// Clear this event lattice and use it as source for next pass.
				el.ClearOccupants()
				prevEvents = nextEvents
			}
		}
	}

done:
	fmt.Printf("\n=== training complete: %d passes ===\n", numPasses)
	return nil
}

// runDetect tests whether sample text bonds through the N-pass lattice chain.
//
// Each pass thaws its trained lattice, clears occupants, and grows input
// through it. The bond events from each pass become the input for the next.
// All lattices are disposable copies — the trained state is never modified.
func runDetect(ctx context.Context, sampleFile, memoryDir string, trollMemoryDir *string, _, maxSteps, numPasses, trollPasses, tokenWindow int) error {
	if numPasses < 1 {
		numPasses = 1
	}

	db, err := memory.Open(memoryDir)
	if err != nil {
		return fmt.Errorf("open memory: %w", err)
	}
	defer db.Close()

	probeCfg := grow.Config{
		MaxSteps: 3,
		Workers:  1,
	}

	// Thaw AI detection lattices.
	lattices, err := thawLattices(db, numPasses, "ai")
	if err != nil {
		return err
	}

	// Print AI lattice info.
	fmt.Println("=== AI detection lattices ===")
	printLatticeInfo(lattices)

	// Thaw troll detection lattices if configured.
	var trollLattices []*lattice.Lattice
	if *trollMemoryDir != "" {
		trollDB, err := memory.Open(*trollMemoryDir)
		if err != nil {
			return fmt.Errorf("open troll memory: %w", err)
		}
		trollLattices, err = thawLattices(trollDB, trollPasses, "troll")
		trollDB.Close()
		if err != nil {
			return err
		}
		fmt.Println("\n=== manipulation detection lattices ===")
		printLatticeInfo(trollLattices)
	}

	// Read and tokenize sample.
	f, err := os.Open(sampleFile)
	if err != nil {
		return err
	}
	defer f.Close()

	rawSolution := enzyme.Text{}.Digest(f)
	solution := limitTokens(rawSolution, tokenWindow)

	// Materialize tokens so both chains can use them.
	var tokens []axiom.Element
	for tok := range solution {
		tokens = append(tokens, tok)
	}

	fmt.Printf("\nsample: %s (%d tokens)\n", sampleFile, len(tokens))

	// --- AI chain ---
	fmt.Println("\n=== AI detection ===")
	aiStats := runChain(ctx, lattices, numPasses, tokens, probeCfg, true)

	// --- Troll chain ---
	var trollStats []grammar.PassStats
	if len(trollLattices) > 0 {
		fmt.Println("\n=== manipulation detection ===")
		trollStats = runChain(ctx, trollLattices, trollPasses, tokens, probeCfg, true)
	}

	// Final verdict.
	verdict := grammar.Score(aiStats)
	if len(trollStats) > 0 {
		grammar.ScoreTroll(&verdict, trollStats)
	}
	fmt.Println()
	fmt.Print(verdict.String())

	return nil
}

// thawLattices loads and thaws a chain of mindsicles from a memory DB.
func thawLattices(db *memory.DB, passes int, label string) ([]*lattice.Lattice, error) {
	lats := make([]*lattice.Lattice, passes)

	wordData, err := db.LoadMindsicle(1)
	if err != nil {
		return nil, fmt.Errorf("load %s word mindsicle: %w", label, err)
	}
	var wm mindsicle.Mindsicle
	if err := json.Unmarshal(wordData, &wm); err != nil {
		return nil, fmt.Errorf("unmarshal %s word mindsicle: %w", label, err)
	}
	lats[0] = wm.Thaw(func(tag string) axiom.Constraint {
		return grammar.NewConstraint(tag, grammar.NaturalText)
	})

	for p := 1; p < passes; p++ {
		data, err := db.LoadMindsicle(uint32(p + 1))
		if err != nil {
			return nil, fmt.Errorf("load %s event mindsicle gen %d: %w", label, p+1, err)
		}
		var em mindsicle.Mindsicle
		if err := json.Unmarshal(data, &em); err != nil {
			return nil, fmt.Errorf("unmarshal %s event mindsicle gen %d: %w", label, p+1, err)
		}
		lats[p] = em.Thaw(func(tag string) axiom.Constraint {
			return grammar.NewConstraint(tag, grammar.BondEvent)
		})
	}

	return lats, nil
}

// printLatticeInfo prints node/occupancy info for a lattice chain.
func printLatticeInfo(lats []*lattice.Lattice) {
	for i, lat := range lats {
		h := lat.Health()
		label := "word"
		if i > 0 {
			label = fmt.Sprintf("event-L%d", i)
		}
		fmt.Printf("  %s: %d nodes, %d/%d occupied\n", label, h.NodeCount, h.Occupied, h.NodeCount)
	}
}

// runChain runs the full N-pass SeqProbe chain and returns pass stats.
// If verbose is true, prints per-pass details to stdout.
func runChain(ctx context.Context, lats []*lattice.Lattice, passes int, tokens []axiom.Element, probeCfg grow.Config, verbose bool) []grammar.PassStats {
	// Pass 1: probe tokens against word lattice.
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
	grow.SeqProbe(ctx, lats[0], tokenCh, probeCfg, eventsCh)
	close(eventsCh)
	<-done

	allStats := make([]grammar.PassStats, passes)
	b, e, t := countEvents(prevEvents)
	allStats[0] = grammar.PassStats{Events: prevEvents, Bonded: b, Expired: e, Total: t}

	if verbose {
		fmt.Printf("\n  pass 1/%d (word):\n", passes)
		fmt.Printf("    tokens:  %d\n", t)
		fmt.Printf("    bonded:  %d (%.2f%%)\n", b, pct(b, t))
		fmt.Printf("    expired: %d (%.2f%%)\n", e, pct(e, t))
		printEventDist(prevEvents, t)
		printWalkStats(prevEvents)
	}

	// Chain passes 2..N.
	for p := 1; p < passes; p++ {
		classified := classifyEvents(prevEvents)
		nextEvents := seqProbeElements(ctx, lats[p], classified, probeCfg)

		b, e, t = countEvents(nextEvents)
		allStats[p] = grammar.PassStats{Events: nextEvents, Bonded: b, Expired: e, Total: t}

		if verbose {
			fmt.Printf("\n  pass %d/%d (event-L%d):\n", p+1, passes, p)
			fmt.Printf("    events:  %d\n", t)
			fmt.Printf("    bonded:  %d (%.2f%%)\n", b, pct(b, t))
			fmt.Printf("    expired: %d (%.2f%%)\n", e, pct(e, t))
			printEventDist(nextEvents, t)
			printWalkStats(nextEvents)
		}

		prevEvents = nextEvents
	}

	return allStats
}

// runFingerprint builds a model-specific lattice from labeled corpus.
func runFingerprint(ctx context.Context, corpusDir, memoryDir, modelName string, latticeSize, maxSteps int) error {
	db, err := memory.Open(memoryDir)
	if err != nil {
		return fmt.Errorf("open memory: %w", err)
	}
	defer db.Close()

	l, _ := buildTextLattice(latticeSize)
	collector := profile.NewCollector()
	tracker := converge.NewTracker(converge.DefaultWindowSize, converge.DefaultThreshold)

	cfg := grow.Config{
		MaxSteps: maxSteps,
		Workers:  grow.WorkerCount(),
	}

	registry := extract.NewRegistry()
	files, err := collectFiles(corpusDir)
	if err != nil {
		return fmt.Errorf("walk corpus: %w", err)
	}

	fmt.Printf("fingerprinting model %q from %d files\n", modelName, len(files))

	for i, path := range files {
		select {
		case <-ctx.Done():
			fmt.Println("\ninterrupted, saving progress...")
			goto save
		default:
		}

		if err := ingestFile(ctx, l, path, registry, cfg, collector, tracker); err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", path, err)
			continue
		}

		if (i+1)%100 == 0 || i == len(files)-1 {
			fmt.Printf("  [%d/%d]\n", i+1, len(files))
		}
	}

save:
	snap := collector.Snapshot()
	stats := profile.Compute(snap, l.Size())
	profData, err := snap.Marshal()
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}
	if err := db.RecordModelSpore(modelName, profData); err != nil {
		return fmt.Errorf("save model spore: %w", err)
	}

	printStats(modelName, stats)
	return nil
}

// buildTextLattice creates a lattice with NaturalText grammar topology.
func buildTextLattice(size int) (*lattice.Lattice, [32]byte) {
	counts := grammar.TextDefaultCounts(size)
	seed := [32]byte{0xDE, 0xAD, 0xBE, 0xEF} // fixed seed for reproducibility
	l := grammar.BuildGrammarLattice(
		grammar.NaturalText,
		counts,
		seed,
		func(tag string) axiom.Constraint {
			return grammar.NewConstraint(tag, grammar.NaturalText)
		},
	)
	return l, seed
}

// buildEventLattice creates a lattice with BondEvent grammar topology.
func buildEventLattice(size int) *lattice.Lattice {
	counts := grammar.EventDefaultCounts(size)
	seed := [32]byte{0xE0, 0xE1, 0x70, 0x02}
	return grammar.BuildGrammarLattice(
		grammar.BondEvent,
		counts,
		seed,
		func(tag string) axiom.Constraint {
			return grammar.NewConstraint(tag, grammar.BondEvent)
		},
	)
}

// ingestFileSimple grows text through the lattice with convergence tracking
// but no profile collection.
func ingestFileSimple(
	ctx context.Context,
	l *lattice.Lattice,
	path string,
	registry *extract.Registry,
	cfg grow.Config,
	tracker *converge.Tracker,
) error {
	rc, err := registry.Extract(path)
	if err != nil {
		return err
	}
	defer rc.Close()

	solution := enzyme.Text{}.Digest(rc)

	counted := make(chan axiom.Element, 64)
	go func() {
		defer close(counted)
		for elem := range solution {
			tracker.RecordToken()
			select {
			case counted <- elem:
			case <-ctx.Done():
				return
			}
		}
	}()

	events := make(chan grow.Event, 256)
	go func() {
		for range events {
		}
	}()

	grow.Run(ctx, l, counted, cfg, events)
	close(events)

	return nil
}

// collectFileEvents grows a file through a lattice and returns all events.
// Caps at maxTokensPerFile tokens to avoid spending forever on large books.
const maxTokensPerFile = 50000

func collectFileEvents(
	ctx context.Context,
	l *lattice.Lattice,
	path string,
	registry *extract.Registry,
	cfg grow.Config,
) ([]grow.Event, error) {
	rc, err := registry.Extract(path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	solution := limitTokens(enzyme.Text{}.Digest(rc), maxTokensPerFile)
	eventsCh := make(chan grow.Event, 256)
	var events []grow.Event

	go func() {
		for ev := range eventsCh {
			events = append(events, ev)
		}
	}()

	grow.Run(ctx, l, solution, cfg, eventsCh)
	close(eventsCh)

	return events, nil
}

// classifyEvents converts grow.Events into axiom.Elements for the next pass.
func classifyEvents(events []grow.Event) []axiom.Element {
	elems := make([]axiom.Element, len(events))
	for i, ev := range events {
		elems[i] = grammar.ClassifyEvent(ev)
	}
	return elems
}

// growElements grows a slice of elements into a lattice, returning all events.
// If dryRun is true, bonds are immediately reversed (for detection).
func growElements(ctx context.Context, l *lattice.Lattice, elems []axiom.Element, cfg grow.Config, dryRun bool) []grow.Event {
	solution := make(chan axiom.Element, 256)
	go func() {
		defer close(solution)
		for _, e := range elems {
			select {
			case solution <- e:
			case <-ctx.Done():
				return
			}
		}
	}()

	eventsCh := make(chan grow.Event, 256)
	var events []grow.Event

	go func() {
		for ev := range eventsCh {
			events = append(events, ev)
		}
	}()

	if dryRun {
		grow.DryRun(ctx, l, solution, cfg, eventsCh)
	} else {
		grow.Run(ctx, l, solution, cfg, eventsCh)
	}
	close(eventsCh)

	return events
}

// countEvents counts bonded/expired/total from an event slice.
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

// saveLattice freezes and saves a lattice as a mindsicle at the given generation.
func saveLattice(db *memory.DB, l *lattice.Lattice, gen uint32) error {
	m := mindsicle.Freeze(l, nil)
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal mindsicle gen %d: %w", gen, err)
	}
	if err := db.RecordMindsicle(gen, data); err != nil {
		return fmt.Errorf("save mindsicle gen %d: %w", gen, err)
	}
	fmt.Printf("  saved lattice gen %d (%d bytes)\n", gen, len(data))
	return nil
}

// seqProbeElements runs SeqProbe over a slice of elements against a trained lattice.
func seqProbeElements(ctx context.Context, l *lattice.Lattice, elems []axiom.Element, cfg grow.Config) []grow.Event {
	solution := make(chan axiom.Element, 256)
	go func() {
		defer close(solution)
		for _, e := range elems {
			select {
			case solution <- e:
			case <-ctx.Done():
				return
			}
		}
	}()

	eventsCh := make(chan grow.Event, 256)
	var events []grow.Event

	go func() {
		for ev := range eventsCh {
			events = append(events, ev)
		}
	}()

	grow.SeqProbe(ctx, l, solution, cfg, eventsCh)
	close(eventsCh)

	return events
}

// printWalkStats prints walk distance statistics for bonded events.
func printWalkStats(events []grow.Event) {
	var totalSteps int64
	var bonded int64
	var maxSteps int
	for _, ev := range events {
		if ev.Type == grow.EventBonded {
			totalSteps += int64(ev.Steps)
			bonded++
			if ev.Steps > maxSteps {
				maxSteps = ev.Steps
			}
		}
	}
	if bonded == 0 {
		fmt.Printf("  walk: no bonded events\n")
		return
	}
	avg := float64(totalSteps) / float64(bonded)
	fmt.Printf("  walk: avg=%.2f max=%d (lower=better fit)\n", avg, maxSteps)
}

// printEventDist prints the hit/miss distribution by element type.
func printEventDist(events []grow.Event, total int64) {
	counts := make(map[string]int64)
	for _, ev := range events {
		e := grammar.ClassifyEvent(ev)
		counts[e.Type()]++
	}

	// Aggregate hits and misses.
	var totalHit, totalMiss int64
	for tag, n := range counts {
		if strings.HasSuffix(tag, ".hit") {
			totalHit += n
		} else {
			totalMiss += n
		}
	}
	fmt.Printf("  hit=%d(%.0f%%) miss=%d(%.0f%%)\n",
		totalHit, pct(totalHit, total), totalMiss, pct(totalMiss, total))

	// Print per-type breakdown if there are misses (interesting case).
	if totalMiss > 0 {
		fmt.Printf("  miss types:")
		for _, tag := range grammar.EventTags() {
			if strings.HasSuffix(tag, ".miss") && counts[tag] > 0 {
				fmt.Printf(" %s=%d", tag, counts[tag])
			}
		}
		fmt.Println()
	}
}

// ingestFile extracts text from a file and feeds it through the lattice
// with profile collection and convergence tracking. Used by fingerprint mode.
func ingestFile(
	ctx context.Context,
	l *lattice.Lattice,
	path string,
	registry *extract.Registry,
	cfg grow.Config,
	collector *profile.Collector,
	tracker *converge.Tracker,
) error {
	rc, err := registry.Extract(path)
	if err != nil {
		return err
	}
	defer rc.Close()

	solution := enzyme.Text{}.Digest(rc)

	counted := make(chan axiom.Element, 64)
	go func() {
		defer close(counted)
		for elem := range solution {
			tracker.RecordToken()
			select {
			case counted <- elem:
			case <-ctx.Done():
				return
			}
		}
	}()

	events := make(chan grow.Event, 256)
	go func() {
		for ev := range events {
			collector.RecordGrowEvent(ev)
		}
	}()

	grow.Run(ctx, l, counted, cfg, events)
	close(events)

	return nil
}

// limitTokens wraps a channel to emit at most maxTokens elements.
// If maxTokens <= 0, all elements pass through.
func limitTokens(in <-chan axiom.Element, maxTokens int) <-chan axiom.Element {
	if maxTokens <= 0 {
		return in
	}
	out := make(chan axiom.Element, cap(in))
	go func() {
		defer close(out)
		count := 0
		for e := range in {
			if count >= maxTokens {
				// Drain remaining input to avoid blocking the producer.
				for range in {
				}
				return
			}
			out <- e
			count++
		}
	}()
	return out
}

// pct computes a percentage, returning 0 for zero denominator.
func pct(num, denom int64) float64 {
	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom) * 100
}

// collectFiles recursively collects file paths from a directory.
func collectFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			// Skip hidden directories.
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip hidden files and very small files.
		if strings.HasPrefix(info.Name(), ".") || info.Size() < 100 {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

// printStats prints a Stats struct in a readable format.
func printStats(label string, s profile.Stats) {
	fmt.Printf("%s:\n", label)
	fmt.Printf("  path_entropy:       %s (%.4f)\n", s.PathEntropy.String(), s.PathEntropy.Float64())
	fmt.Printf("  surprisal_variance: %s (%.4f)\n", s.SurprisalVariance.String(), s.SurprisalVariance.Float64())
	fmt.Printf("  burstiness_gini:    %s (%.4f)\n", s.BurstinessGini.String(), s.BurstinessGini.Float64())
	fmt.Printf("  vertex_coverage:    %s (%.4f)\n", s.VertexCoverage.String(), s.VertexCoverage.Float64())
	fmt.Printf("  avg_walk_distance:  %s (%.4f)\n", s.AvgWalkDistance.String(), s.AvgWalkDistance.Float64())
	fmt.Printf("  bond_rate:          %s (%.4f)\n", s.BondRate.String(), s.BondRate.Float64())
	fmt.Printf("  new_vertex_rate:    %s (%.6f)\n", s.NewVertexRate.String(), s.NewVertexRate.Float64())
	fmt.Printf("  transition_entropy: %s (%.4f)\n", s.TransitionEntropy.String(), s.TransitionEntropy.Float64())

	// Also output machine-readable JSON.
	if data, err := json.Marshal(s); err == nil {
		fmt.Printf("  json: %s\n", string(data))
	}
}

