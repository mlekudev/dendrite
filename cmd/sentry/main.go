// Command sentry watches a Nostr relay for kind-1 text notes,
// runs each through the dendrite 8-pass lattice detection chain,
// and broadcasts verdict replies to every known relay from a fresh
// throwaway npub.
//
// Relay discovery: subscribes to kind-10002 (NIP-65) events to
// continuously harvest relay URLs. Verdicts are blasted to every
// relay that will accept them.
//
// Each verdict is signed by a unique, single-use keypair — there is
// no persistent bot identity to mute or flag.
//
// Usage:
//
//	sentry --watch wss://relay.orly.dev --memory .recognise_db
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/mlekudev/dendrite/pkg/nostr"
)

// sentrySignature is a string present in every verdict reply's content.
// Events containing this string are our own verdicts — skip them to avoid
// self-detection loops. Using content rather than tags because tags can
// be spoofed by anyone to evade detection.
const sentrySignature = "https://git.nostrdev.com/mleku/dendrite"

func main() {
	var (
		watchURL      = flag.String("watch", "wss://relay.orly.dev", "relay to subscribe for events")
		memoryDir     = flag.String("memory", ".recognise_db", "badger DB with trained mindsicles")
		trollMemory   = flag.String("troll-memory", "", "badger DB with manipulation-trained mindsicles (empty = disabled)")
		trollPasses   = flag.Int("troll-passes", 8, "number of troll detection passes")
		trollThreshold = flag.Float64("troll-threshold", 0.05, "minimum troll score to include in verdict")
		passes        = flag.Int("passes", 8, "number of detection passes")
		window        = flag.Int("window", 500, "max tokens per sample")
		workers       = flag.Int("workers", 4, "concurrent detector workers")
		rateLimit     = flag.Int("rate-limit", 6, "max verdicts per minute")
		minContent    = flag.Int("min-content", 50, "minimum content length to analyze")
		threshold     = flag.Float64("threshold", 0.5, "minimum confidence to publish verdict")
		maxRelays     = flag.Int("max-relays", 1000, "max relay URLs to discover")
		verbose       = flag.Bool("verbose", false, "log detection details")
	)
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Load trained lattices.
	log.Printf("loading %d-pass lattice chain from %s", *passes, *memoryDir)
	detector, err := NewDetector(*memoryDir, *passes, *window)
	if err != nil {
		log.Fatalf("init detector: %v", err)
	}
	log.Printf("lattices loaded (%d passes, %d token window)", *passes, *window)

	// Load troll detection lattices if configured.
	if *trollMemory != "" {
		log.Printf("loading %d-pass troll lattice chain from %s", *trollPasses, *trollMemory)
		if err := detector.LoadTrollLattices(*trollMemory, *trollPasses); err != nil {
			log.Fatalf("init troll detector: %v", err)
		}
		log.Printf("troll lattices loaded (%d passes)", *trollPasses)
	}

	// Relay pool: seeds + discovered from kind-10002 events.
	pool := NewRelayPool(seedRelays, *maxRelays)
	log.Printf("relay pool seeded with %d relays", pool.Size())

	// Upload binary to blossom servers for content-addressable distribution.
	log.Println("uploading sentry binary to blossom servers...")
	blob, err := UploadSelf(ctx)
	if err != nil {
		log.Printf("blossom upload failed (continuing without): %v", err)
	}

	// Broadcaster fans out to entire pool.
	broadcaster := NewBroadcaster(pool, blob)

	// Rate limiter.
	limiter := NewRateLimiter(*rateLimit)
	defer limiter.Stop()

	// Work channel for detector workers.
	work := make(chan *nostr.Event, 64)

	// Start detector workers.
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for ev := range work {
				verdict := detector.Detect(ctx, ev.Content)
				if *verbose {
					label := verdict.Label
					if verdict.TrollLabel != "" {
						label += " | " + verdict.TrollLabel
					}
					log.Printf("worker %d: event %s → %s", id, ev.ID[:12], label)
				}
				aiTriggered := !verdict.Human && verdict.Confidence >= *threshold
				trollTriggered := verdict.TrollScore >= *trollThreshold && verdict.TrollLabel != ""
				if aiTriggered || trollTriggered {
					if limiter.Allow() {
						reason := verdict.Label
						if trollTriggered {
							reason += " | " + verdict.TrollLabel
						}
						log.Printf("detection: event %s by %s — %s",
							ev.ID[:12], ev.Pubkey[:12], reason)
						broadcaster.PublishVerdict(ctx, ev, verdict)
					} else if *verbose {
						log.Printf("rate limited: skipping verdict for %s", ev.ID[:12])
					}
				}
			}
		}(i)
	}

	// Watch relay with reconnection.
	log.Printf("watching %s for kind-1 + kind-10002 events", *watchURL)
	watchLoop(ctx, *watchURL, work, pool, *minContent, *verbose)

	close(work)
	wg.Wait()
	log.Println("shutdown complete")
}

// watchLoop subscribes to kind-1 (text notes) and kind-10002 (relay lists)
// on the watch relay. Text notes go to the work channel for detection.
// Relay lists feed the relay pool for broadcast discovery.
func watchLoop(ctx context.Context, url string, work chan<- *nostr.Event, pool *RelayPool, minContent int, verbose bool) {
	backoff := time.Second
	lastPoolLog := time.Time{}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		client, err := nostr.Connect(ctx, url)
		if err != nil {
			log.Printf("connect %s: %v (retry in %v)", url, err, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		log.Printf("connected to %s", url)

		// Subscribe to kind-1 (text) and kind-10002 (relay lists).
		if err := client.Subscribe(ctx, "sentry", nostr.Filter{
			Kinds: []int{1, 10002},
		}); err != nil {
			log.Printf("subscribe %s: %v", url, err)
			client.Disconnect()
			continue
		}

		listenDone := make(chan error, 1)
		go func() {
			listenDone <- client.Listen(ctx)
		}()

		func() {
			for {
				select {
				case <-ctx.Done():
					client.Disconnect()
					return
				case err := <-listenDone:
					log.Printf("disconnected from %s: %v", url, err)
					return
				case ev := <-client.Events:
					if ev == nil {
						continue
					}

					// Relay list: feed to pool for discovery.
					if ev.Kind == 10002 {
						pool.Ingest(ev)
						if time.Since(lastPoolLog) > 10*time.Second {
							log.Printf("relay pool: %d relays discovered", pool.Size())
							lastPoolLog = time.Now()
						}
						continue
					}

					// Text note: feed to detector workers.
					// Skip our own verdict replies. We identify them
					// by a signature string in the content rather than
					// by tag, because tags can be spoofed by anyone.
					if strings.Contains(ev.Content, sentrySignature) {
						continue
					}
					if len(ev.Content) < minContent {
						continue
					}
					select {
					case work <- ev:
					default:
						if verbose {
							log.Printf("backpressure: dropping %s", ev.ID[:12])
						}
					}
				}
			}
		}()

		client.Disconnect()
		log.Printf("reconnecting to %s in %v", url, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
