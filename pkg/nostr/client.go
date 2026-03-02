package nostr

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/coder/websocket"
)

// OKResponse is the relay's response to an EVENT submission.
type OKResponse struct {
	EventID  string
	Accepted bool
	Message  string
}

// Client connects to a Nostr relay and handles subscriptions.
type Client struct {
	URL  string
	conn *websocket.Conn
	mu   sync.Mutex

	// Events receives validated events from active subscriptions.
	Events chan *Event

	// Notices receives NOTICE messages from the relay.
	Notices chan string

	// OKs receives OK responses from the relay after EVENT submissions.
	OKs chan OKResponse
}

// Connect establishes a WebSocket connection to the relay.
func Connect(ctx context.Context, url string) (*Client, error) {
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	conn.SetReadLimit(1 << 20) // 1MB

	c := &Client{
		URL:     url,
		conn:    conn,
		Events:  make(chan *Event, 256),
		Notices: make(chan string, 16),
		OKs:     make(chan OKResponse, 16),
	}

	return c, nil
}

// Subscribe sends a REQ message to the relay.
func (c *Client) Subscribe(ctx context.Context, subID string, filters ...Filter) error {
	msg := make([]any, 0, 2+len(filters))
	msg = append(msg, "REQ", subID)
	for _, f := range filters {
		msg = append(msg, f)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, data)
}

// Close sends a CLOSE message for a subscription.
func (c *Client) CloseSubscription(ctx context.Context, subID string) error {
	msg, _ := json.Marshal([]string{"CLOSE", subID})
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, msg)
}

// Publish sends an EVENT message to the relay.
func (c *Client) Publish(ctx context.Context, event *Event) error {
	msg, _ := json.Marshal([]any{"EVENT", event})
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, msg)
}

// Listen reads messages from the relay and dispatches them.
// It blocks until the context is cancelled or the connection closes.
// Events are validated before being sent to the Events channel.
func (c *Client) Listen(ctx context.Context) error {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return err
		}

		// Parse the envelope: ["TYPE", ...]
		var envelope []json.RawMessage
		if json.Unmarshal(data, &envelope) != nil || len(envelope) < 2 {
			continue
		}

		var msgType string
		if json.Unmarshal(envelope[0], &msgType) != nil {
			continue
		}

		switch msgType {
		case "EVENT":
			// ["EVENT", "<sub_id>", <event>]
			if len(envelope) < 3 {
				continue
			}
			var event Event
			if json.Unmarshal(envelope[2], &event) != nil {
				continue
			}
			// Validate before delivering.
			if event.Valid() {
				select {
				case c.Events <- &event:
				default:
					// Events channel full — drop.
				}
			}

		case "EOSE":
			// End of stored events — we don't need to act on this yet.

		case "NOTICE":
			if len(envelope) >= 2 {
				var notice string
				if json.Unmarshal(envelope[1], &notice) == nil {
					select {
					case c.Notices <- notice:
					default:
					}
				}
			}

		case "OK":
			// ["OK", "<event_id>", <accepted>, "<message>"]
			if len(envelope) >= 4 {
				var eventID string
				var accepted bool
				var message string
				json.Unmarshal(envelope[1], &eventID)
				json.Unmarshal(envelope[2], &accepted)
				json.Unmarshal(envelope[3], &message)
				select {
				case c.OKs <- OKResponse{EventID: eventID, Accepted: accepted, Message: message}:
				default:
				}
			}

		case "CLOSED":
			// Subscription closed by relay.
		}
	}
}

// Disconnect closes the WebSocket connection.
func (c *Client) Disconnect() {
	c.conn.Close(websocket.StatusNormalClosure, "")
}
