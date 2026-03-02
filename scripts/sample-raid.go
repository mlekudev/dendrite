//go:build ignore

// sample-raid extracts a balanced subset from the full RAID CSV.
// It takes N samples per model (non-adversarial, non-human only),
// preserving the original header. Output goes to stdout.
//
// Usage:
//
//	go run scripts/sample-raid.go -input raid-train.csv -per-model 100 > raid-sample.csv
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"sort"
)

func main() {
	input := flag.String("input", "", "path to full RAID CSV")
	perModel := flag.Int("per-model", 100, "samples per model")
	minLen := flag.Int("min-len", 100, "minimum generation length")
	seed := flag.Int64("seed", 42, "random seed for reproducibility")
	flag.Parse()

	if *input == "" {
		log.Fatal("usage: go run sample-raid.go -input raid-train.csv [-per-model 100]")
	}

	f, err := os.Open(*input)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		log.Fatal(err)
	}

	col := map[string]int{}
	for i, name := range header {
		col[name] = i
	}

	modelIdx := col["model"]
	attackIdx := col["attack"]
	genIdx := col["generation"]

	// Collect all non-adversarial, non-human rows grouped by model.
	byModel := map[string][][]string{}
	total := 0
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		total++
		if len(record) <= genIdx {
			continue
		}
		if record[attackIdx] != "none" || record[modelIdx] == "human" {
			continue
		}
		if len(record[genIdx]) < *minLen {
			continue
		}
		byModel[record[modelIdx]] = append(byModel[record[modelIdx]], record)
	}

	log.Printf("parsed %d rows, found %d models", total, len(byModel))

	// Sort model names for deterministic output.
	var models []string
	for m := range byModel {
		models = append(models, m)
	}
	sort.Strings(models)

	// Sample.
	rng := rand.New(rand.NewSource(*seed))
	w := csv.NewWriter(os.Stdout)
	w.Write(header)

	sampled := 0
	for _, model := range models {
		rows := byModel[model]
		rng.Shuffle(len(rows), func(i, j int) {
			rows[i], rows[j] = rows[j], rows[i]
		})
		n := *perModel
		if n > len(rows) {
			n = len(rows)
		}
		for _, row := range rows[:n] {
			w.Write(row)
			sampled++
		}
		log.Printf("  %s: %d/%d sampled", model, n, len(rows))
	}

	w.Flush()
	if err := w.Error(); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "total: %d samples from %d models\n", sampled, len(models))
}
