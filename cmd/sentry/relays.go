package main

import (
	"log"
	"strings"
	"sync"

	"github.com/mlekudev/dendrite/pkg/nostr"
)

// RelayPool discovers and maintains a set of known relay URLs
// by harvesting kind-10002 (NIP-65 relay list) events.
type RelayPool struct {
	mu     sync.RWMutex
	relays map[string]struct{}
	max    int
}

// NewRelayPool creates a pool seeded with initial relay URLs.
func NewRelayPool(seeds []string, max int) *RelayPool {
	if max < 1 {
		max = 1000
	}
	p := &RelayPool{
		relays: make(map[string]struct{}, max),
		max:    max,
	}
	for _, u := range seeds {
		u = normalizeURL(u)
		if u != "" {
			p.relays[u] = struct{}{}
		}
	}
	return p
}

// Ingest processes a kind-10002 relay list event, extracting relay URLs.
// NIP-65 format: tags are ["r", "<url>"] or ["r", "<url>", "read"|"write"].
// We want write-capable relays (no marker = both, "write" = write-only).
func (p *RelayPool) Ingest(ev *nostr.Event) {
	if ev.Kind != 10002 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, tag := range ev.Tags {
		if len(tag) < 2 || tag[0] != "r" {
			continue
		}
		url := normalizeURL(tag[1])
		if url == "" {
			continue
		}
		// Skip read-only relays — we need to write to them.
		if len(tag) >= 3 && tag[2] == "read" {
			continue
		}
		if len(p.relays) >= p.max {
			return // cap reached
		}
		p.relays[url] = struct{}{}
	}
}

// URLs returns all known relay URLs.
func (p *RelayPool) URLs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	urls := make([]string, 0, len(p.relays))
	for u := range p.relays {
		urls = append(urls, u)
	}
	return urls
}

// Size returns the number of known relays.
func (p *RelayPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.relays)
}

// normalizeURL cleans up a relay WebSocket URL.
func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	// Accept wss:// and ws:// only.
	if !strings.HasPrefix(u, "wss://") && !strings.HasPrefix(u, "ws://") {
		return ""
	}
	// Strip trailing slash.
	u = strings.TrimRight(u, "/")
	// Skip localhost/private relays.
	lower := strings.ToLower(u)
	if strings.Contains(lower, "localhost") || strings.Contains(lower, "127.0.0.1") {
		return ""
	}
	return u
}

// Well-known relays to seed the pool.
var seedRelays = []string{
	"wss://relay.orly.dev",
	"wss://relay.damus.io",
	"wss://nos.lol",
	"wss://relay.nostr.band",
	"wss://relay.snort.social",
	"wss://nostr.wine",
	"wss://relay.primal.net",
	"wss://nostr-pub.wellorder.net",
	"wss://nostr.mutinywallet.com",
	"wss://purplepag.es",
	"wss://relay.nostr.bg",
}

func init() {
	log.SetFlags(log.Ldate | log.Ltime)
}
