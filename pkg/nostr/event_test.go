package nostr

import (
	"testing"
)

func TestEventCanonicalAndID(t *testing.T) {
	// Create an event and sign it, then verify the round-trip.
	e := &Event{
		CreatedAt: 1700000000,
		Kind:      1,
		Tags:      [][]string{},
		Content:   "hello nostr",
	}

	// Use a test private key (NOT for production).
	testPriv := "0000000000000000000000000000000000000000000000000000000000000001"
	if err := e.Sign(testPriv); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// ID should be set.
	if e.ID == "" {
		t.Fatal("expected non-empty ID after signing")
	}

	// Pubkey should be set.
	if e.Pubkey == "" {
		t.Fatal("expected non-empty pubkey after signing")
	}
	if len(e.Pubkey) != 64 {
		t.Fatalf("expected 64-char hex pubkey, got %d chars", len(e.Pubkey))
	}

	// Sig should be set.
	if e.Sig == "" {
		t.Fatal("expected non-empty sig after signing")
	}
	if len(e.Sig) != 128 {
		t.Fatalf("expected 128-char hex sig, got %d chars", len(e.Sig))
	}

	// ID should match recomputation.
	if !e.ValidID() {
		t.Error("ValidID returned false")
	}

	// Signature should be valid.
	if !e.ValidSig() {
		t.Error("ValidSig returned false")
	}

	// Full validation.
	if !e.Valid() {
		t.Error("Valid returned false")
	}
}

func TestEventInvalidID(t *testing.T) {
	e := &Event{
		ID:        "0000000000000000000000000000000000000000000000000000000000000000",
		Pubkey:    "0000000000000000000000000000000000000000000000000000000000000001",
		CreatedAt: 1700000000,
		Kind:      1,
		Tags:      [][]string{},
		Content:   "hello",
		Sig:       "0000000000000000000000000000000000000000000000000000000000000000" + "0000000000000000000000000000000000000000000000000000000000000000",
	}

	if e.ValidID() {
		t.Error("expected ValidID to return false for wrong ID")
	}
}

func TestEventTampered(t *testing.T) {
	e := &Event{
		CreatedAt: 1700000000,
		Kind:      1,
		Tags:      [][]string{},
		Content:   "original",
	}

	testPriv := "0000000000000000000000000000000000000000000000000000000000000001"
	if err := e.Sign(testPriv); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if !e.Valid() {
		t.Fatal("expected valid before tampering")
	}

	// Tamper with content.
	e.Content = "tampered"

	// ID should no longer match.
	if e.ValidID() {
		t.Error("expected ValidID to return false after tampering")
	}
}
