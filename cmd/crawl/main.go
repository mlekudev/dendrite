// Command crawl scans Nostr relay history and runs each kind-1 text note
// through the dendrite detection chain. Outputs timestamped CSV for plotting
// AI text and manipulation penetration over time.
//
// The output CSV has columns:
//
//	timestamp,event_id,pubkey,human,confidence,deep_walk,long_miss,troll_score,label,troll_label
//
// Usage:
//
//	crawl -relay wss://relay.orly.dev -memory .recognise_db \
//	  -troll-memory .troll_db -output crawl-results.csv
//
// Resume from where you left off:
//
//	crawl -relay wss://relay.orly.dev -memory .recognise_db \
//	  -since 1700000000 -output crawl-results.csv
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/mlekudev/dendrite/pkg/detect"
	"github.com/mlekudev/dendrite/pkg/nostr"
)

func main() {
	var (
		relayURL    = flag.String("relay", "wss://relay.orly.dev", "relay to crawl")
		memoryDir   = flag.String("memory", ".recognise_db", "badger DB with trained snapshot")
		trollMemory = flag.String("troll-memory", "", "badger DB with manipulation-trained snapshot")
		window      = flag.Int("window", 500, "max tokens per sample")
		since       = flag.Int64("since", 0, "start timestamp (unix seconds, 0 = relay's earliest)")
		until       = flag.Int64("until", 0, "end timestamp (0 = now)")
		chunk       = flag.Int("chunk", 86400, "time window per request in seconds (default: 1 day)")
		timeout     = flag.Int("timeout", 30, "seconds to wait for events per chunk")
		minContent  = flag.Int("min-content", 50, "minimum content length to analyze")
		output      = flag.String("output", "", "output CSV path (default: stdout)")
		verbose     = flag.Bool("verbose", false, "log each event to stderr")
	)
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Load detector.
	log.Printf("loading Cayley tree detector from %s", *memoryDir)
	detector, err := detect.NewDetector(*memoryDir, *window)
	if err != nil {
		log.Fatalf("init detector: %v", err)
	}

	if *trollMemory != "" {
		log.Printf("loading troll Cayley tree from %s", *trollMemory)
		if err := detector.LoadTrollTree(*trollMemory); err != nil {
			log.Fatalf("init troll detector: %v", err)
		}
	}

	// Output CSV.
	var out *os.File
	if *output != "" {
		out, err = os.OpenFile(*output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("open output: %v", err)
		}
		defer out.Close()
	} else {
		out = os.Stdout
	}

	w := csv.NewWriter(out)
	defer w.Flush()

	// Write header only if file is empty/new.
	info, _ := out.Stat()
	if info == nil || info.Size() == 0 {
		w.Write([]string{
			"timestamp", "event_id", "pubkey",
			"human", "confidence", "deep_walk", "long_miss",
			"troll_score", "label", "troll_label",
		})
		w.Flush()
	}

	// Time range.
	start := *since
	end := *until
	if end == 0 {
		end = time.Now().Unix()
	}

	log.Printf("crawling %s from %s to %s (chunk=%ds)",
		*relayURL,
		time.Unix(start, 0).UTC().Format("2006-01-02"),
		time.Unix(end, 0).UTC().Format("2006-01-02"),
		*chunk)

	totalEvents := 0
	totalDetected := 0
	chunkDur := int64(*chunk)
	chunkTimeout := time.Duration(*timeout) * time.Second

	for cursor := start; cursor < end; cursor += chunkDur {
		select {
		case <-ctx.Done():
			log.Printf("interrupted at timestamp %d", cursor)
			goto done
		default:
		}

		chunkEnd := cursor + chunkDur
		if chunkEnd > end {
			chunkEnd = end
		}

		events, err := fetchChunk(ctx, *relayURL, cursor, chunkEnd, *minContent, chunkTimeout)
		if err != nil {
			log.Printf("chunk %d-%d: %v", cursor, chunkEnd, err)
			continue
		}

		if len(events) == 0 {
			if *verbose {
				log.Printf("chunk %s: 0 events",
					time.Unix(cursor, 0).UTC().Format("2006-01-02"))
			}
			continue
		}

		for _, ev := range events {
			verdict := detector.Detect(ctx, ev.Content)
			totalEvents++
			if !verdict.Human {
				totalDetected++
			}

			w.Write([]string{
				strconv.FormatInt(ev.CreatedAt, 10),
				ev.ID,
				ev.Pubkey,
				strconv.FormatBool(verdict.Human),
				fmt.Sprintf("%.4f", verdict.Confidence),
				fmt.Sprintf("%.4f", verdict.DeepWalk),
				fmt.Sprintf("%.4f", verdict.LongMissRate),
				fmt.Sprintf("%.4f", verdict.TrollScore),
				verdict.Label,
				verdict.TrollLabel,
			})

			if *verbose {
				log.Printf("  %s %s %s",
					time.Unix(ev.CreatedAt, 0).UTC().Format("2006-01-02 15:04"),
					ev.ID[:12], verdict.Label)
			}
		}
		w.Flush()

		log.Printf("chunk %s: %d events (%d total, %.1f%% AI so far)",
			time.Unix(cursor, 0).UTC().Format("2006-01-02"),
			len(events), totalEvents,
			float64(totalDetected)/float64(totalEvents)*100)
	}

done:
	log.Printf("crawl complete: %d events, %d classified as AI (%.1f%%)",
		totalEvents, totalDetected,
		safePct(totalDetected, totalEvents))
}

// fetchChunk connects to the relay, subscribes for kind-1 events in the
// given time range, collects them until EOSE or timeout, and returns them.
func fetchChunk(ctx context.Context, url string, since, until int64, minContent int, timeout time.Duration) ([]*nostr.Event, error) {
	connCtx, connCancel := context.WithTimeout(ctx, 10*time.Second)
	defer connCancel()

	client, err := nostr.Connect(connCtx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()

	// Start listener.
	listenDone := make(chan error, 1)
	go func() {
		listenDone <- client.Listen(ctx)
	}()

	// Subscribe with time range.
	if err := client.Subscribe(ctx, "crawl", nostr.Filter{
		Kinds: []int{1},
		Since: &since,
		Until: &until,
	}); err != nil {
		return nil, fmt.Errorf("subscribe: %w", err)
	}

	// Collect events until timeout or disconnect.
	var events []*nostr.Event
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case ev := <-client.Events:
			if ev == nil {
				continue
			}
			if len(ev.Content) >= minContent {
				events = append(events, ev)
			}
		case <-timer.C:
			return events, nil
		case err := <-listenDone:
			if err != nil {
				return events, fmt.Errorf("listen: %w", err)
			}
			return events, nil
		case <-ctx.Done():
			return events, ctx.Err()
		}
	}
}

func safePct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}
