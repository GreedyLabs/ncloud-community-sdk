// Retry/backoff transport for the NCP SDK.
//
// Wraps another http.RoundTripper and retries the inner call when it
// returns a transient failure — defined as a network error or one of a
// configurable set of HTTP status codes (5xx, 429 by default). Each
// retry waits for an exponentially-growing duration with full jitter
// (the AWS-recommended algorithm), bounded by MaxDelay. Honors
// `Retry-After` when the server provides it on a 429 / 503.
//
// Idempotency safety: by default only GET, HEAD, PUT, DELETE, OPTIONS
// requests are retried. POST and PATCH are NOT retried because they
// are not generally safe — a server-side state change followed by the
// connection dying could turn a single user intent into two. Callers
// who know a particular POST is safe (e.g. one with an idempotency
// token) can pass RetryAllMethods to opt in.

package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"time"
)

// RetryConfig tunes RetryTransport. Zero values pick safe defaults:
//
//	MaxRetries        = 3
//	InitialDelay      = 100 ms
//	MaxDelay          = 30 s
//	RetryStatusCodes  = {429, 500, 502, 503, 504}  (NOT 501 — typically permanent)
//	RetryAllMethods   = false  (POST/PATCH are skipped)
type RetryConfig struct {
	MaxRetries       int           // attempt the request up to MaxRetries+1 times total
	InitialDelay     time.Duration // first backoff
	MaxDelay         time.Duration // cap for any single backoff
	RetryStatusCodes []int         // HTTP statuses that should trigger a retry
	RetryAllMethods  bool          // allow retry on POST / PATCH too
}

// DefaultRetryConfig returns a config tuned for typical NCP usage.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:       3,
		InitialDelay:     100 * time.Millisecond,
		MaxDelay:         30 * time.Second,
		RetryStatusCodes: []int{429, 500, 502, 503, 504},
	}
}

// RetryTransport wraps Next and applies the retry policy from Config.
//
// It is safe to nest RetryTransport between auth and ensureJSONResponse
// (or any other middleware) — every retry re-invokes the entire Next
// chain, so the inner auth signer regenerates the timestamp / signature
// and ensureJSONResponse rebuilds the body if it mutated it. The wrapped
// request's body is restored from req.GetBody() before each retry; if
// GetBody is nil and the body is non-nil, the request cannot be retried
// safely and the first response is returned as-is.
type RetryTransport struct {
	Config RetryConfig
	Next   http.RoundTripper
}

func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cfg := t.Config
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 100 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 30 * time.Second
	}
	if len(cfg.RetryStatusCodes) == 0 {
		cfg.RetryStatusCodes = []int{429, 500, 502, 503, 504}
	}
	next := t.Next
	if next == nil {
		next = http.DefaultTransport
	}

	if !methodIsRetriable(req.Method, cfg.RetryAllMethods) {
		return next.RoundTrip(req)
	}
	// If the request has a body but no GetBody, we can't replay it on
	// retry — bail out of the retry loop after the first attempt.
	canReplayBody := req.Body == nil || req.GetBody != nil

	var lastResp *http.Response
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// Refresh the body so the inner signer can re-read it.
			if req.GetBody != nil {
				body, gerr := req.GetBody()
				if gerr != nil {
					return lastResp, fmt.Errorf("retry: refresh body: %w", gerr)
				}
				req.Body = body
			}
		}

		// Drain + close the previous response body so the connection can
		// be reused. If we don't, a 500 with a chunked body holds the
		// connection until GC.
		if lastResp != nil && lastResp.Body != nil {
			_, _ = io.Copy(io.Discard, lastResp.Body)
			_ = lastResp.Body.Close()
		}

		lastResp, lastErr = next.RoundTrip(req)

		// Decide whether to retry.
		retry := false
		switch {
		case lastErr != nil:
			// Network-level failure — context cancellation never retries.
			if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
				return lastResp, lastErr
			}
			retry = true
		case lastResp != nil && containsInt(cfg.RetryStatusCodes, lastResp.StatusCode):
			retry = true
		}
		if !retry {
			return lastResp, lastErr
		}
		if attempt == cfg.MaxRetries {
			break // out of retries — fall through and return the last result
		}
		if !canReplayBody {
			break // can't replay the body — give up after attempt 0
		}

		// Compute backoff: exponential with full jitter, capped at MaxDelay.
		// Honor Retry-After if the server gave us one.
		var delay time.Duration
		if lastResp != nil {
			delay = parseRetryAfter(lastResp.Header.Get("Retry-After"))
		}
		if delay <= 0 {
			delay = backoffWithJitter(cfg.InitialDelay, cfg.MaxDelay, attempt)
		}
		// Sleep, but stay cancellation-aware.
		select {
		case <-time.After(delay):
		case <-req.Context().Done():
			return lastResp, req.Context().Err()
		}
	}
	return lastResp, lastErr
}

// methodIsRetriable returns true when the HTTP method is generally
// considered idempotent OR the user explicitly opted into retrying
// every method.
func methodIsRetriable(method string, allMethods bool) bool {
	if allMethods {
		return true
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

func containsInt(haystack []int, needle int) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// parseRetryAfter understands both forms in RFC 7231 §7.1.3 — a non-
// negative integer of seconds, or an HTTP-date.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

// backoffWithJitter returns a duration on [0, base*2^attempt], capped at
// max. AWS-style "full jitter" — the actual wait is uniform-random in
// the window — which spreads retries across many concurrent clients
// better than fixed exponential backoff.
//
// `attempt` is 0-based; the first retry uses 2^0 = 1× the initial delay.
func backoffWithJitter(initial, max time.Duration, attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	// Compute base * 2^attempt without overflow.
	cap64 := int64(max)
	cur := int64(initial)
	for i := 0; i < attempt; i++ {
		cur <<= 1
		if cur >= cap64 || cur <= 0 {
			cur = cap64
			break
		}
	}
	if cur > cap64 {
		cur = cap64
	}
	if cur <= 0 {
		return 0
	}
	// Crypto/rand is overkill for jitter but the API is convenient and
	// avoids the global math/rand seed concern.
	n, err := rand.Int(rand.Reader, big.NewInt(cur))
	if err != nil {
		return time.Duration(cur)
	}
	return time.Duration(n.Int64())
}
