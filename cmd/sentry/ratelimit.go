package main

import "time"

// RateLimiter is a token bucket that allows a fixed number of actions per minute.
type RateLimiter struct {
	tokens chan struct{}
	ticker *time.Ticker
}

// NewRateLimiter creates a rate limiter allowing perMinute actions per minute.
func NewRateLimiter(perMinute int) *RateLimiter {
	if perMinute < 1 {
		perMinute = 1
	}
	interval := time.Minute / time.Duration(perMinute)
	rl := &RateLimiter{
		tokens: make(chan struct{}, perMinute),
		ticker: time.NewTicker(interval),
	}
	// Start with one token available.
	rl.tokens <- struct{}{}

	go func() {
		for range rl.ticker.C {
			select {
			case rl.tokens <- struct{}{}:
			default: // bucket full
			}
		}
	}()
	return rl
}

// Allow returns true if an action is permitted, consuming one token.
func (rl *RateLimiter) Allow() bool {
	select {
	case <-rl.tokens:
		return true
	default:
		return false
	}
}

// Stop releases the ticker.
func (rl *RateLimiter) Stop() {
	rl.ticker.Stop()
}
