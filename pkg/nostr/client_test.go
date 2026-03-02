package nostr

import (
	"context"
	"testing"
	"time"
)

func TestClientLiveRelay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live relay test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Connect to ORLY relay.
	client, err := Connect(ctx, "wss://relay.orly.dev")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect()

	// Start listening in background.
	listenErr := make(chan error, 1)
	go func() {
		listenErr <- client.Listen(ctx)
	}()

	// Subscribe to recent kind 1 events with a limit.
	limit := 5
	err = client.Subscribe(ctx, "test-sub", Filter{
		Kinds: []int{1},
		Limit: &limit,
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Collect events.
	var events []*Event
	timeout := time.After(8 * time.Second)
	for len(events) < limit {
		select {
		case ev := <-client.Events:
			events = append(events, ev)
			t.Logf("event: kind=%d pubkey=%s...%s content=%.40s",
				ev.Kind, ev.Pubkey[:8], ev.Pubkey[56:], ev.Content)
		case <-timeout:
			t.Logf("timeout after receiving %d events", len(events))
			goto done
		}
	}
done:

	if len(events) == 0 {
		t.Fatal("received no events from relay")
	}

	// All events should be valid (they passed validation in Listen).
	for i, ev := range events {
		if !ev.ValidID() {
			t.Errorf("event %d: invalid ID", i)
		}
		if !ev.ValidSig() {
			t.Errorf("event %d: invalid signature", i)
		}
	}

	t.Logf("received and validated %d events", len(events))
}
