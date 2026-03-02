package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mlekudev/dendrite/pkg/grammar"
	"github.com/mlekudev/dendrite/pkg/nostr"
)

// Broadcaster publishes verdict events to every relay in the pool.
type Broadcaster struct {
	pool *RelayPool
	blob *BlobInfo // may be nil if blossom upload failed
}

// NewBroadcaster creates a broadcaster backed by the relay pool.
func NewBroadcaster(pool *RelayPool, blob *BlobInfo) *Broadcaster {
	return &Broadcaster{pool: pool, blob: blob}
}

// PublishVerdict composes a verdict reply, signs it with a fresh throwaway
// keypair, and blasts it to every known relay. The private key is discarded
// after signing. Each verdict has a unique, unmutable identity.
func (b *Broadcaster) PublishVerdict(ctx context.Context, original *nostr.Event, verdict grammar.Verdict) {
	id, err := nostr.NewIdentity()
	if err != nil {
		log.Printf("keygen: %v", err)
		return
	}

	// Tag the original event as a reply. Include the watch relay as hint.
	tags := [][]string{
		{"e", original.ID, "wss://relay.orly.dev", "reply"},
		{"p", original.Pubkey},
		{"t", "dendrite"},
		{"t", "ai-detection"},
	}
	if verdict.TrollLabel != "" {
		tags = append(tags, []string{"t", "troll-detection"})
	}
	ev := &nostr.Event{
		CreatedAt: time.Now().Unix(),
		Kind:      1,
		Tags:      tags,
		Content:   b.verdictContent(verdict),
	}

	if err := ev.Sign(id.PrivKeyHex()); err != nil {
		log.Printf("sign verdict: %v", err)
		return
	}

	urls := b.pool.URLs()

	// Log the verdict as a nevent URI so operators can look it up directly.
	nevent, err := nostr.Nevent(ev.ID, []string{"wss://relay.orly.dev"}, ev.Pubkey, ev.Kind)
	if err != nil {
		log.Printf("broadcasting verdict %s to %d relays for event %s",
			ev.ID[:12], len(urls), original.ID[:12])
	} else {
		log.Printf("verdict nostr:%s", nevent)
		log.Printf("broadcasting to %d relays for event %s", len(urls), original.ID[:12])
	}

	// Fan out to all relays concurrently.
	var accepted, rejected, failed atomic.Int32
	var wg sync.WaitGroup

	for _, url := range urls {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			publishOne(ctx, url, ev, &accepted, &rejected, &failed)
		}(url)
	}

	// Wait for all publishes to complete (or timeout).
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
	}

	origNevent, _ := nostr.Nevent(original.ID, []string{"wss://relay.orly.dev"}, original.Pubkey, original.Kind)
	log.Printf("result: accepted=%d rejected=%d failed=%d (of %d relays) re: nostr:%s",
		accepted.Load(), rejected.Load(), failed.Load(), len(urls), origNevent)
}

// verdictContent composes the full verdict text including source and blob links.
func (b *Broadcaster) verdictContent(v grammar.Verdict) string {
	var sb strings.Builder
	sb.WriteString(v.String())
	if b.blob != nil && b.blob.SHA256 != "" {
		fmt.Fprintf(&sb, "binary sha256: %s\n", b.blob.SHA256)
		if len(b.blob.URLs) > 0 {
			fmt.Fprintf(&sb, "download: %s\n", b.blob.URLs[0])
		}
	}
	return sb.String()
}

func publishOne(ctx context.Context, url string, ev *nostr.Event, accepted, rejected, failed *atomic.Int32) {
	pubCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	c, err := nostr.Connect(pubCtx, url)
	if err != nil {
		failed.Add(1)
		return
	}
	defer c.Disconnect()

	go c.Listen(pubCtx)

	if err := c.Publish(pubCtx, ev); err != nil {
		failed.Add(1)
		return
	}

	select {
	case ok := <-c.OKs:
		if ok.Accepted {
			accepted.Add(1)
		} else {
			rejected.Add(1)
		}
	case <-pubCtx.Done():
		failed.Add(1)
	}
}
