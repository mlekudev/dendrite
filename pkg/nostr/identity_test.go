package nostr

import (
	"testing"
)

func TestNewIdentity(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	if len(id.PubKey) != 64 {
		t.Fatalf("expected 64-char hex pubkey, got %d chars", len(id.PubKey))
	}
	if len(id.PrivKeyHex()) != 64 {
		t.Fatalf("expected 64-char hex privkey, got %d chars", len(id.PrivKeyHex()))
	}
}

func TestComposeMetadata(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}

	ev, err := id.ComposeMetadata(2, 5, "abcdef1234567890abcdef1234567890")
	if err != nil {
		t.Fatalf("ComposeMetadata: %v", err)
	}

	if ev.Kind != 0 {
		t.Fatalf("expected kind 0, got %d", ev.Kind)
	}
	if ev.Pubkey != id.PubKey {
		t.Fatalf("pubkey mismatch: %s != %s", ev.Pubkey, id.PubKey)
	}
	if !ev.Valid() {
		t.Fatal("metadata event failed validation")
	}
}

func TestComposeStatus(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}

	ev, err := id.ComposeStatus(1, 3, 2941, 987, 0.336, 0.284, 2)
	if err != nil {
		t.Fatalf("ComposeStatus: %v", err)
	}

	if ev.Kind != 1 {
		t.Fatalf("expected kind 1, got %d", ev.Kind)
	}
	if !ev.Valid() {
		t.Fatal("status event failed validation")
	}

	// Check hashtags.
	foundDendrite := false
	foundGen := false
	for _, tag := range ev.Tags {
		if len(tag) >= 2 && tag[0] == "t" {
			if tag[1] == "dendrite" {
				foundDendrite = true
			}
			if tag[1] == "gen3" {
				foundGen = true
			}
		}
	}
	if !foundDendrite {
		t.Fatal("missing #dendrite tag")
	}
	if !foundGen {
		t.Fatal("missing #gen3 tag")
	}
}

func TestTwoIdentitiesDistinct(t *testing.T) {
	id1, _ := NewIdentity()
	id2, _ := NewIdentity()
	if id1.PubKey == id2.PubKey {
		t.Fatal("two identities should have distinct pubkeys")
	}
}
