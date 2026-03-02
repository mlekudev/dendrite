// Command benchmark runs the dendrite 8-pass lattice detection chain
// against three categories of text:
//
//  1. Known-human texts (e.g. Gutenberg corpus)
//  2. Claude-generated texts (local .txt files by model tier)
//  3. RAID benchmark corpus (labeled CSV, multiple LLM models)
//
// Usage:
//
//	benchmark -memory .recognise_db \
//	  -corpus ./gutenberg_dammit/corpus/gutenberg-dammit-files \
//	  -claude-corpus ./gutenberg_dammit/claude_corpus \
//	  -raid ./gutenberg_dammit/raid-train.csv
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mlekudev/dendrite/pkg/detect"
	"github.com/mlekudev/dendrite/pkg/grammar"
)

// progressOut is the destination for interactive phase progress lines.
// Defaults to stdout; summary mode redirects it to stderr.
var progressOut io.Writer = os.Stdout

func main() {
	var (
		memoryDir    = flag.String("memory", ".recognise_db", "badger DB with trained mindsicles")
		corpusDir    = flag.String("corpus", "", "directory of .txt files (known human)")
		claudeDir    = flag.String("claude-corpus", "", "directory with model-tier subdirs of .txt files")
		raidCSV      = flag.String("raid", "", "path to RAID train.csv (labeled AI text)")
		raidSamples  = flag.Int("raid-samples", 50, "samples per RAID model (0 = all)")
		passes      = flag.Int("passes", 8, "number of detection passes")
		window      = flag.Int("window", 500, "max tokens per sample")
		workers     = flag.Int("workers", 0, "concurrent workers (0 = NumCPU)")
		maxFiles    = flag.Int("max", 0, "max corpus files to process (0 = all)")
		minBytes    = flag.Int("min-bytes", 200, "skip corpus files smaller than this")
		trollMemory = flag.String("troll-memory", "", "badger DB with manipulation-trained mindsicles (empty = disabled)")
		trollPasses = flag.Int("troll-passes", 8, "number of troll detection passes")
		verbose     = flag.Bool("verbose", false, "print per-file verdicts")
		summary      = flag.Bool("summary", false, "print markdown summary (for grant applications)")
	)
	flag.Parse()

	if *workers <= 0 {
		*workers = runtime.NumCPU()
	}

	// In summary mode, redirect interactive progress to stderr so that
	// stdout contains only the markdown report (pipe-friendly).
	if *summary {
		progressOut = os.Stderr
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Load detector.
	log.Printf("loading %d-pass lattice chain from %s", *passes, *memoryDir)
	detector, err := detect.NewDetector(*memoryDir, *passes, *window)
	if err != nil {
		log.Fatalf("init detector: %v", err)
	}
	log.Printf("lattices loaded (%d passes, %d token window)", *passes, *window)

	// Load troll detection lattices if configured.
	trollEnabled := false
	if *trollMemory != "" {
		log.Printf("loading %d-pass troll lattice chain from %s", *trollPasses, *trollMemory)
		if err := detector.LoadTrollLattices(*trollMemory, *trollPasses); err != nil {
			log.Fatalf("init troll detector: %v", err)
		}
		log.Printf("troll lattices loaded (%d passes)", *trollPasses)
		trollEnabled = true
	}

	// ── Phase 1: Human corpus ──────────────────────────────────────────
	var humanResults []result
	if *corpusDir != "" {
		files, err := collectFiles(*corpusDir, *maxFiles, *minBytes)
		if err != nil {
			log.Fatalf("collect files: %v", err)
		}
		log.Printf("found %d text files in %s", len(files), *corpusDir)

		if len(files) > 0 {
			fmt.Fprintln(progressOut)
			fmt.Fprintln(progressOut, "━━━ PHASE 1: HUMAN TEXT (Gutenberg) ━━━")
			humanResults = runCorpusBenchmark(ctx, detector, files, *workers, *verbose)
		}
	}

	// ── Phase 2: Claude corpus (local .txt files) ──────────────────────
	var claudeResults []modelResults
	if *claudeDir != "" {
		fmt.Fprintln(progressOut)
		fmt.Fprintln(progressOut, "━━━ PHASE 2: CLAUDE TEXT (local corpus) ━━━")
		claudeResults = runClaudeCorpus(ctx, detector, *claudeDir, *verbose)
	}

	// ── Phase 3: RAID corpus (CSV) ─────────────────────────────────────
	var raidResults []modelResults
	if *raidCSV != "" {
		fmt.Fprintln(progressOut)
		fmt.Fprintln(progressOut, "━━━ PHASE 3: RAID CORPUS (11 LLM models) ━━━")
		raidResults = runRAIDBenchmark(ctx, detector, *raidCSV, *raidSamples, *verbose)
	}

	// ── Final report ───────────────────────────────────────────────────
	fmt.Fprintln(progressOut)
	fmt.Fprintln(progressOut)
	if *summary {
		printMarkdownSummary(humanResults, claudeResults, raidResults, trollEnabled)
	} else {
		printFinalReport(humanResults, claudeResults, raidResults, trollEnabled)
	}
}

// result captures a single text's detection outcome.
type result struct {
	source   string // file path or "model:prompt_idx"
	verdict  grammar.Verdict
	duration time.Duration
	err      error
}

// modelResults groups results by LLM model.
type modelResults struct {
	model   string
	results []result
}

func collectFiles(dir string, maxFiles, minBytes int) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".txt") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() < int64(minBytes) {
			return nil
		}
		files = append(files, path)
		if maxFiles > 0 && len(files) >= maxFiles {
			return fs.SkipAll
		}
		return nil
	})
	return files, err
}

func runCorpusBenchmark(ctx context.Context, detector *detect.Detector, files []string, workers int, verbose bool) []result {
	work := make(chan string, workers*2)
	results := make([]result, len(files))
	var processed atomic.Int64
	total := int64(len(files))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range work {
				select {
				case <-ctx.Done():
					return
				default:
				}

				data, err := os.ReadFile(path)
				if err != nil {
					idx := processed.Add(1) - 1
					results[idx] = result{source: path, err: err}
					continue
				}

				start := time.Now()
				verdict := detector.Detect(ctx, string(data))
				dur := time.Since(start)

				idx := processed.Add(1) - 1
				results[idx] = result{
					source:   path,
					verdict:  verdict,
					duration: dur,
				}

				if verbose {
					troll := trollSuffix(verdict)
					fmt.Fprintf(progressOut, "  [%d/%d] %s → %s%s (%.0fms)\n",
						idx+1, total, filepath.Base(path), verdict.Label, troll, float64(dur.Milliseconds()))
				} else if (idx+1)%10 == 0 || idx+1 == total {
					fmt.Fprintf(progressOut, "\r  processed %d/%d files...", idx+1, total)
				}
			}
		}()
	}

outer:
	for _, f := range files {
		select {
		case work <- f:
		case <-ctx.Done():
			break outer
		}
	}
	close(work)
	wg.Wait()

	if !verbose {
		fmt.Fprintln(progressOut)
	}

	n := int(processed.Load())
	return results[:n]
}

// runClaudeCorpus scans subdirectories of claudeDir. Each subdir is a
// model tier (e.g., "haiku_style", "sonnet_style", "opus_style").
// Files within are .txt samples to detect.
func runClaudeCorpus(ctx context.Context, detector *detect.Detector, claudeDir string, verbose bool) []modelResults {
	entries, err := os.ReadDir(claudeDir)
	if err != nil {
		log.Printf("read claude corpus dir: %v", err)
		return nil
	}

	var all []modelResults

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		tierName := entry.Name()
		tierPath := filepath.Join(claudeDir, tierName)

		files, err := collectFiles(tierPath, 0, 10)
		if err != nil {
			log.Printf("collect %s: %v", tierName, err)
			continue
		}
		if len(files) == 0 {
			continue
		}

		fmt.Fprintf(progressOut, "\n  ┌─ %s (%d samples)\n", tierName, len(files))

		var results []result
	claudeLoop:
		for i, path := range files {
			select {
			case <-ctx.Done():
				break claudeLoop
			default:
			}

			data, err := os.ReadFile(path)
			if err != nil {
				results = append(results, result{source: path, err: err})
				continue
			}

			start := time.Now()
			verdict := detector.Detect(ctx, string(data))
			dur := time.Since(start)

			r := result{
				source:   path,
				verdict:  verdict,
				duration: dur,
			}
			results = append(results, r)

			if verbose {
				troll := trollSuffix(verdict)
				fmt.Fprintf(progressOut, "  │  %s → %s%s (walk=%.3f miss=%.3f)\n",
					filepath.Base(path), verdict.Label, troll, verdict.DeepWalk, verdict.LongMissRate)
			} else {
				fmt.Fprintf(progressOut, "  │  [%d/%d] %s → %s\n", i+1, len(files), filepath.Base(path), verdict.Label)
			}
		}

		detected, total := 0, 0
		for _, r := range results {
			if r.err != nil {
				continue
			}
			total++
			if !r.verdict.Human {
				detected++
			}
		}
		if total > 0 {
			fmt.Fprintf(progressOut, "  └─ detected %d/%d as AI (%.0f%%)\n",
				detected, total, float64(detected)/float64(total)*100)
		}

		all = append(all, modelResults{model: tierName, results: results})
	}

	return all
}

// raidSample holds a single row from the RAID CSV (non-adversarial only).
type raidSample struct {
	model      string
	domain     string
	generation string
}

// runRAIDBenchmark reads the RAID CSV, filters for attack=none and
// non-human rows, samples per model, and detects each sample.
func runRAIDBenchmark(ctx context.Context, detector *detect.Detector, csvPath string, samplesPerModel int, verbose bool) []modelResults {
	log.Printf("loading RAID corpus from %s", csvPath)

	f, err := os.Open(csvPath)
	if err != nil {
		log.Printf("open RAID CSV: %v", err)
		return nil
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1 // variable fields (some rows malformed)

	// Read header.
	header, err := reader.Read()
	if err != nil {
		log.Printf("read RAID header: %v", err)
		return nil
	}

	// Map column names to indices.
	col := map[string]int{}
	for i, name := range header {
		col[name] = i
	}

	modelIdx := col["model"]
	attackIdx := col["attack"]
	genIdx := col["generation"]
	domainIdx := col["domain"]

	// Collect non-adversarial, non-human samples grouped by model.
	byModel := map[string][]raidSample{}
	rowCount := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed rows
		}
		rowCount++

		if len(record) <= genIdx {
			continue
		}

		attack := record[attackIdx]
		model := record[modelIdx]
		if attack != "none" || model == "human" {
			continue
		}

		gen := record[genIdx]
		if len(gen) < 100 {
			continue // skip very short generations
		}

		domain := ""
		if domainIdx < len(record) {
			domain = record[domainIdx]
		}

		byModel[model] = append(byModel[model], raidSample{
			model:      model,
			domain:     domain,
			generation: gen,
		})
	}
	log.Printf("parsed %d rows, found %d models with clean samples", rowCount, len(byModel))

	// Sample and detect.
	var all []modelResults

	for model, samples := range byModel {
		select {
		case <-ctx.Done():
			return all
		default:
		}

		// Shuffle and take subset.
		if samplesPerModel > 0 && len(samples) > samplesPerModel {
			rand.Shuffle(len(samples), func(i, j int) {
				samples[i], samples[j] = samples[j], samples[i]
			})
			samples = samples[:samplesPerModel]
		}

		fmt.Fprintf(progressOut, "\n  ┌─ %s (%d samples)\n", model, len(samples))

		var results []result
	raidLoop:
		for i, s := range samples {
			select {
			case <-ctx.Done():
				break raidLoop
			default:
			}

			start := time.Now()
			verdict := detector.Detect(ctx, s.generation)
			dur := time.Since(start)

			r := result{
				source:   fmt.Sprintf("raid:%s:%s:%d", model, s.domain, i),
				verdict:  verdict,
				duration: dur,
			}
			results = append(results, r)

			if verbose {
				troll := trollSuffix(verdict)
				fmt.Fprintf(progressOut, "  │  [%d/%d] %s %s%s (walk=%.3f miss=%.3f)\n",
					i+1, len(samples), s.domain, verdict.Label, troll, verdict.DeepWalk, verdict.LongMissRate)
			} else if (i+1)%10 == 0 || i+1 == len(samples) {
				fmt.Fprintf(progressOut, "\r  │  processed %d/%d...", i+1, len(samples))
			}
		}

		if !verbose {
			fmt.Fprintln(progressOut)
		}

		detected, total := 0, 0
		for _, r := range results {
			if r.err != nil {
				continue
			}
			total++
			if !r.verdict.Human {
				detected++
			}
		}
		if total > 0 {
			fmt.Fprintf(progressOut, "  └─ detected %d/%d as AI (%.0f%%)\n",
				detected, total, float64(detected)/float64(total)*100)
		}

		all = append(all, modelResults{model: model, results: results})
	}

	return all
}

func printFinalReport(humanResults []result, claudeResults, raidResults []modelResults, trollEnabled bool) {
	fmt.Println("╔═══════════════════════════════════════════════════════════════╗")
	fmt.Println("║            DENDRITE DETECTION BENCHMARK REPORT               ║")
	fmt.Println("╠═══════════════════════════════════════════════════════════════╣")

	allAI := append(claudeResults, raidResults...)

	// Human section.
	if len(humanResults) > 0 {
		stats := computeStats(humanResults)
		fmt.Println("║                                                               ║")
		fmt.Println("║  HUMAN TEXT (known-human corpus)                               ║")
		fmt.Println("║  ─────────────────────────────────────────────────────────     ║")
		fmt.Printf("║  samples:         %-44d║\n", stats.total)
		fmt.Printf("║  correct (HUMAN): %-5d  (%.1f%%)%-31s║\n", stats.humanCount, stats.accuracy, "")
		fmt.Printf("║  false positive:  %-5d  (%.1f%% misclassified as AI)%-14s║\n", stats.aiCount, 100-stats.accuracy, "")
		fmt.Printf("║  avg deep walk:   %.3f  (boundary: 1.320)%-18s║\n", stats.avgWalk, "")
		fmt.Printf("║  avg long-miss:   %.3f  (boundary: 0.310)%-18s║\n", stats.avgMiss, "")
		fmt.Printf("║  avg time:        %.0f ms/sample%-30s║\n", stats.avgDurMs, "")
		if trollEnabled {
			fmt.Printf("║  avg troll score: %.3f%-38s║\n", stats.avgTroll, "")
		}
	}

	// Claude section.
	if len(claudeResults) > 0 {
		fmt.Println("║                                                               ║")
		fmt.Println("║  CLAUDE TEXT (Opus 4.6 / Sonnet / Haiku styles)               ║")
		fmt.Println("║  ─────────────────────────────────────────────────────────     ║")
		printModelSection(claudeResults, trollEnabled)
	}

	// RAID section.
	if len(raidResults) > 0 {
		fmt.Println("║                                                               ║")
		fmt.Println("║  RAID CORPUS (labeled multi-model AI text)                    ║")
		fmt.Println("║  ─────────────────────────────────────────────────────────     ║")
		printModelSection(raidResults, trollEnabled)
	}

	// Troll leaderboard: rank all models by avg troll score (highest first).
	if trollEnabled && len(allAI) > 0 {
		fmt.Println("║                                                               ║")
		fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
		fmt.Println("║  MANIPULATION SCORE LEADERBOARD                               ║")
		fmt.Println("║  ─────────────────────────────────────────────────────────     ║")

		type trollEntry struct {
			model    string
			avgScore float64
			samples  int
		}
		var entries []trollEntry
		for _, mr := range allAI {
			var sum float64
			var n int
			for _, r := range mr.results {
				if r.err != nil {
					continue
				}
				sum += r.verdict.TrollScore
				n++
			}
			if n > 0 {
				entries = append(entries, trollEntry{mr.model, sum / float64(n), n})
			}
		}
		// Sort descending by avgScore.
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[j].avgScore > entries[i].avgScore {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}
		for _, e := range entries {
			bar := strings.Repeat("█", int(e.avgScore*20))
			fmt.Printf("║  %-20s %.1f%% %s%-*s║\n",
				e.model, e.avgScore*100, bar, 20-int(e.avgScore*20), "")
		}
		// Human baseline if available.
		if len(humanResults) > 0 {
			hStats := computeStats(humanResults)
			bar := strings.Repeat("░", int(hStats.avgTroll*20))
			fmt.Printf("║  %-20s %.1f%% %s%-*s(human baseline)  ║\n",
				"human", hStats.avgTroll*100, bar, 20-int(hStats.avgTroll*20), "")
		}
	}

	// Combined verdict.
	if len(humanResults) > 0 && len(allAI) > 0 {
		hStats := computeStats(humanResults)
		totalDetected, totalSamples := 0, 0
		for _, mr := range allAI {
			for _, r := range mr.results {
				if r.err != nil {
					continue
				}
				totalSamples++
				if !r.verdict.Human {
					totalDetected++
				}
			}
		}

		aiDetectRate := 0.0
		if totalSamples > 0 {
			aiDetectRate = float64(totalDetected) / float64(totalSamples) * 100
		}

		fmt.Println("║                                                               ║")
		fmt.Println("╠═══════════════════════════════════════════════════════════════╣")
		fmt.Println("║  COMBINED ACCURACY                                            ║")
		fmt.Println("║  ─────────────────────────────────────────────────────────     ║")
		fmt.Printf("║  human correct:   %5.1f%%  (true negative rate)%-15s║\n", hStats.accuracy, "")
		fmt.Printf("║  AI detected:     %5.1f%%  (true positive rate)%-15s║\n", aiDetectRate, "")
		balanced := (hStats.accuracy + aiDetectRate) / 2
		fmt.Printf("║  balanced acc:    %5.1f%%%-37s║\n", balanced, "")
	}

	fmt.Println("║                                                               ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════╝")
}

func printModelSection(results []modelResults, trollEnabled bool) {
	var totalDetected, totalSamples int
	for _, mr := range results {
		detected, samples := 0, 0
		var walkSum, missSum, trollSum float64
		for _, r := range mr.results {
			if r.err != nil {
				continue
			}
			samples++
			if !r.verdict.Human {
				detected++
			}
			walkSum += r.verdict.DeepWalk
			missSum += r.verdict.LongMissRate
			trollSum += r.verdict.TrollScore
		}
		totalDetected += detected
		totalSamples += samples

		if samples > 0 {
			trollStr := ""
			if trollEnabled {
				trollStr = fmt.Sprintf(" troll=%.1f%%", trollSum/float64(samples)*100)
			}
			fmt.Printf("║  %-20s %2d/%-3d detected  (walk=%.3f miss=%.3f%s)  ║\n",
				mr.model, detected, samples,
				walkSum/float64(samples), missSum/float64(samples), trollStr)
		}
	}

	if totalSamples > 0 {
		fmt.Println("║  ─────────────────────────────────────────────────────────     ║")
		fmt.Printf("║  TOTAL:            %3d/%-4d  (%.1f%%)%-26s║\n",
			totalDetected, totalSamples,
			float64(totalDetected)/float64(totalSamples)*100, "")
	}
}

type stats struct {
	total      int
	humanCount int
	aiCount    int
	errors     int
	accuracy   float64
	avgConf    float64
	avgWalk    float64
	avgMiss    float64
	avgTroll   float64
	avgDurMs   float64
}

func computeStats(results []result) stats {
	var s stats
	for _, r := range results {
		if r.err != nil {
			s.errors++
			continue
		}
		s.total++
		if r.verdict.Human {
			s.humanCount++
		} else {
			s.aiCount++
		}
		s.avgConf += r.verdict.Confidence
		s.avgWalk += r.verdict.DeepWalk
		s.avgMiss += r.verdict.LongMissRate
		s.avgTroll += r.verdict.TrollScore
		s.avgDurMs += float64(r.duration.Milliseconds())
	}
	if s.total > 0 {
		s.accuracy = float64(s.humanCount) / float64(s.total) * 100
		s.avgConf /= float64(s.total)
		s.avgWalk /= float64(s.total)
		s.avgMiss /= float64(s.total)
		s.avgTroll /= float64(s.total)
		s.avgDurMs /= float64(s.total)
	}
	return s
}

// trollSuffix returns a display suffix for troll detection results.
// Returns empty string when no troll label is present.
func trollSuffix(v grammar.Verdict) string {
	if v.TrollLabel != "" {
		return " | " + v.TrollLabel
	}
	return ""
}

// printMarkdownSummary emits a clean markdown report suitable for pasting
// into grant applications, project pages, or documentation. No box-drawing
// characters, no ANSI escapes.
func printMarkdownSummary(humanResults []result, claudeResults, raidResults []modelResults, trollEnabled bool) {
	allAI := append(claudeResults, raidResults...)

	fmt.Println("# Dendrite Detection Benchmark")
	fmt.Println()
	fmt.Println("Deterministic lattice-based AI text detection. No neural networks, no GPU.")
	fmt.Println()

	// ── Combined accuracy headline ──
	if len(humanResults) > 0 && len(allAI) > 0 {
		hStats := computeStats(humanResults)
		totalDetected, totalSamples := 0, 0
		for _, mr := range allAI {
			for _, r := range mr.results {
				if r.err != nil {
					continue
				}
				totalSamples++
				if !r.verdict.Human {
					totalDetected++
				}
			}
		}
		aiRate := 0.0
		if totalSamples > 0 {
			aiRate = float64(totalDetected) / float64(totalSamples) * 100
		}
		balanced := (hStats.accuracy + aiRate) / 2

		fmt.Println("## Overall Accuracy")
		fmt.Println()
		fmt.Println("| Metric | Value |")
		fmt.Println("|--------|-------|")
		fmt.Printf("| Human correct (true negative) | %.1f%% |\n", hStats.accuracy)
		fmt.Printf("| AI detected (true positive) | %.1f%% |\n", aiRate)
		fmt.Printf("| **Balanced accuracy** | **%.1f%%** |\n", balanced)
		fmt.Println()
	}

	// ── Human corpus ──
	if len(humanResults) > 0 {
		hs := computeStats(humanResults)
		fmt.Println("## Human Text (Gutenberg Corpus)")
		fmt.Println()
		fmt.Printf("- **%d** samples, **%.1f%%** correctly identified as human\n", hs.total, hs.accuracy)
		fmt.Printf("- Avg deep walk: %.3f (boundary: 1.320)\n", hs.avgWalk)
		fmt.Printf("- Avg long-miss rate: %.3f (boundary: 0.310)\n", hs.avgMiss)
		fmt.Printf("- Avg detection time: %.0f ms/sample\n", hs.avgDurMs)
		if trollEnabled {
			fmt.Printf("- Avg manipulation score: %.1f%% (human baseline)\n", hs.avgTroll*100)
		}
		fmt.Println()
	}

	// ── AI model table ──
	if len(allAI) > 0 {
		fmt.Println("## AI Detection by Model")
		fmt.Println()
		if trollEnabled {
			fmt.Println("| Model | Detected | Rate | Avg Walk | Avg Miss | Manipulation |")
			fmt.Println("|-------|----------|------|----------|----------|-------------|")
		} else {
			fmt.Println("| Model | Detected | Rate | Avg Walk | Avg Miss |")
			fmt.Println("|-------|----------|------|----------|----------|")
		}

		totalDetected, totalSamples := 0, 0
		for _, mr := range allAI {
			detected, samples := 0, 0
			var walkSum, missSum, trollSum float64
			for _, r := range mr.results {
				if r.err != nil {
					continue
				}
				samples++
				if !r.verdict.Human {
					detected++
				}
				walkSum += r.verdict.DeepWalk
				missSum += r.verdict.LongMissRate
				trollSum += r.verdict.TrollScore
			}
			totalDetected += detected
			totalSamples += samples
			if samples > 0 {
				rate := float64(detected) / float64(samples) * 100
				if trollEnabled {
					fmt.Printf("| %s | %d/%d | %.0f%% | %.3f | %.3f | %.1f%% |\n",
						mr.model, detected, samples, rate,
						walkSum/float64(samples), missSum/float64(samples),
						trollSum/float64(samples)*100)
				} else {
					fmt.Printf("| %s | %d/%d | %.0f%% | %.3f | %.3f |\n",
						mr.model, detected, samples, rate,
						walkSum/float64(samples), missSum/float64(samples))
				}
			}
		}
		if totalSamples > 0 {
			if trollEnabled {
				fmt.Printf("| **Total** | **%d/%d** | **%.1f%%** | | | |\n",
					totalDetected, totalSamples,
					float64(totalDetected)/float64(totalSamples)*100)
			} else {
				fmt.Printf("| **Total** | **%d/%d** | **%.1f%%** | | |\n",
					totalDetected, totalSamples,
					float64(totalDetected)/float64(totalSamples)*100)
			}
		}
		fmt.Println()
	}

	// ── Manipulation leaderboard ──
	if trollEnabled && len(allAI) > 0 {
		type trollEntry struct {
			model    string
			avgScore float64
		}
		var entries []trollEntry
		for _, mr := range allAI {
			var sum float64
			var n int
			for _, r := range mr.results {
				if r.err != nil {
					continue
				}
				sum += r.verdict.TrollScore
				n++
			}
			if n > 0 {
				entries = append(entries, trollEntry{mr.model, sum / float64(n)})
			}
		}
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[j].avgScore > entries[i].avgScore {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}

		fmt.Println("## Manipulation Score Leaderboard")
		fmt.Println()
		fmt.Println("Structural match against a manipulation/rhetoric lattice.")
		fmt.Println("Higher = more manipulation-like prosodic patterns.")
		fmt.Println()
		fmt.Println("| Rank | Model | Score |")
		fmt.Println("|------|-------|-------|")
		for i, e := range entries {
			fmt.Printf("| %d | %s | %.1f%% |\n", i+1, e.model, e.avgScore*100)
		}
		if len(humanResults) > 0 {
			hStats := computeStats(humanResults)
			fmt.Printf("| -- | *human baseline* | *%.1f%%* |\n", hStats.avgTroll*100)
		}
		fmt.Println()
	}

	fmt.Println("---")
	fmt.Println("*Generated by `dendrite benchmark`. Deterministic, reproducible, no GPU required.*")
}
