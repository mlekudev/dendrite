package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mlekudev/dendrite/pkg/nostr"
)

// BlobInfo holds the hash and URLs of a successfully uploaded binary blob.
type BlobInfo struct {
	SHA256 string   // hex-encoded sha256 of the binary
	URLs   []string // blossom URLs where the blob was accepted
}

// Well-known public blossom servers that accept uploads from any npub.
var blossomServers = []string{
	"https://blossom.primal.net",
	"https://blossom.nostr.build",
	"https://cdn.satellite.earth",
	"https://cdn.nostrcheck.me",
	"https://blosstr.com",
	"https://files.v0l.io",
	"https://nostrmedia.com",
}

// UploadSelf reads the running binary from /proc/self/exe (Linux),
// computes its sha256, and uploads it to all known blossom servers.
// Returns blob info with the hash and successful upload URLs.
func UploadSelf(ctx context.Context) (*BlobInfo, error) {
	// Read our own binary.
	binary, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		return nil, fmt.Errorf("read self: %w", err)
	}

	h := sha256.Sum256(binary)
	hexHash := hex.EncodeToString(h[:])
	log.Printf("sentry binary: %d bytes, sha256 %s", len(binary), hexHash)

	// Generate a throwaway identity for the upload auth.
	id, err := nostr.NewIdentity()
	if err != nil {
		return nil, fmt.Errorf("keygen for blossom: %w", err)
	}

	info := &BlobInfo{SHA256: hexHash}
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, server := range blossomServers {
		wg.Add(1)
		go func(server string) {
			defer wg.Done()
			url, err := uploadBlob(ctx, server, binary, hexHash, id)
			if err != nil {
				log.Printf("blossom %s: %v", server, err)
				return
			}
			mu.Lock()
			info.URLs = append(info.URLs, url)
			mu.Unlock()
			log.Printf("blossom %s: uploaded → %s", server, url)
		}(server)
	}

	wg.Wait()
	log.Printf("blossom upload: %d/%d servers accepted", len(info.URLs), len(blossomServers))
	return info, nil
}

// uploadBlob uploads binary data to a single blossom server using BUD-02.
func uploadBlob(ctx context.Context, server string, data []byte, hexHash string, id *nostr.Identity) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Compose kind-24242 authorization event (BUD-02).
	authEvent := &nostr.Event{
		CreatedAt: time.Now().Unix(),
		Kind:      24242,
		Tags: [][]string{
			{"t", "upload"},
			{"x", hexHash},
			{"expiration", fmt.Sprintf("%d", time.Now().Add(5*time.Minute).Unix())},
		},
		Content: "sentry binary upload",
	}
	if err := authEvent.Sign(id.PrivKeyHex()); err != nil {
		return "", fmt.Errorf("sign auth: %w", err)
	}

	// Base64-encode the auth event for the Authorization header.
	authJSON, _ := json.Marshal(authEvent)
	authB64 := base64.StdEncoding.EncodeToString(authJSON)

	req, err := http.NewRequestWithContext(ctx, "PUT", server+"/upload", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Nostr "+authB64)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		// Truncate error body for logging.
		msg := string(body)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}

	// Parse blob descriptor response.
	var desc struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(body, &desc); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if desc.URL != "" {
		return desc.URL, nil
	}
	// Fallback: construct URL from server + hash.
	return fmt.Sprintf("%s/%s", server, hexHash), nil
}
