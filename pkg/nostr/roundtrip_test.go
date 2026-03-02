package nostr

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestRoundTrip publishes events to a public relay and verifies they
// come back unchanged. This is the Stage 5b coherence test.
//
// Uses relay.damus.io which accepts events without auth.
func TestRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live relay test in short mode")
	}

	relayURL := "wss://relay.damus.io"

	// Generate identity.
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}

	// Compose events.
	meta, err := id.ComposeMetadata(99, 7, "deadbeef12345678")
	if err != nil {
		t.Fatal(err)
	}
	status, err := id.ComposeStatus(99, 7, 2922, 980, 0.335, 0.284, 3)
	if err != nil {
		t.Fatal(err)
	}

	// Self-validate.
	if !meta.Valid() {
		t.Fatal("metadata event invalid before publish")
	}
	if !status.Valid() {
		t.Fatal("status event invalid before publish")
	}

	published := []*Event{meta, status}

	// Publish.
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pubCancel()

	client, err := Connect(pubCtx, relayURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect()

	go client.Listen(pubCtx)

	for _, ev := range published {
		if err := client.Publish(pubCtx, ev); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// Wait for OK responses.
	accepted := 0
	for i := 0; i < len(published); i++ {
		select {
		case ok := <-client.OKs:
			if !ok.Accepted {
				t.Fatalf("relay rejected event %s: %s", ok.EventID[:16], ok.Message)
			}
			t.Logf("relay accepted: %s...", ok.EventID[:16])
			accepted++
		case <-time.After(5 * time.Second):
			t.Log("no OK response (timeout) — relay may not send OK")
			goto subscribe
		}
	}

subscribe:
	if accepted == 0 {
		t.Skip("relay did not confirm acceptance — cannot verify round-trip")
	}

	// Wait a moment for relay to process.
	time.Sleep(500 * time.Millisecond)

	// Subscribe back for our own events.
	rtCtx, rtCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer rtCancel()

	rtClient, err := Connect(rtCtx, relayURL)
	if err != nil {
		t.Fatalf("roundtrip connect: %v", err)
	}
	defer rtClient.Disconnect()

	go rtClient.Listen(rtCtx)

	limit := 10
	if err := rtClient.Subscribe(rtCtx, "roundtrip", Filter{
		Authors: []string{id.PubKey},
		Limit:   &limit,
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Collect received events.
	want := make(map[string]*Event, len(published))
	for _, ev := range published {
		want[ev.ID] = ev
	}

	matched := 0
	deadline := time.After(5 * time.Second)
	for len(want) > 0 {
		select {
		case ev := <-rtClient.Events:
			if orig, ok := want[ev.ID]; ok {
				// Byte-level comparison via JSON.
				origJSON, _ := json.Marshal(orig)
				recvJSON, _ := json.Marshal(ev)
				if string(origJSON) != string(recvJSON) {
					t.Errorf("DIVERGENCE on %s...\norig: %s\nrecv: %s",
						ev.ID[:16], origJSON, recvJSON)
				} else {
					t.Logf("matched: %s...", ev.ID[:16])
					matched++
				}
				delete(want, ev.ID)
			}
		case <-deadline:
			goto done
		}
	}

done:
	t.Logf("round-trip: %d/%d matched, %d not returned", matched, len(published), len(want))
	if matched == len(published) {
		t.Log("ZERO DIVERGENCE — round-trip coherent")
	} else if matched > 0 {
		t.Log("partial round-trip — some events matched")
	} else {
		t.Error("no events returned — round-trip failed")
	}
}
