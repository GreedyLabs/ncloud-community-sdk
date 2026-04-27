package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// recordingRT is a stub RoundTripper that returns scripted responses in
// order. Useful for verifying retry behaviour without a real server.
type recordingRT struct {
	calls atomic.Int64
	resps []func() (*http.Response, error)
}

func (r *recordingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	idx := r.calls.Add(1) - 1
	if int(idx) >= len(r.resps) {
		return nil, fmt.Errorf("recordingRT: no scripted response at call #%d", idx)
	}
	return r.resps[idx]()
}

func mkResp(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Body:       http.NoBody,
		Header:     http.Header{},
	}
}

func TestRetry_503ThenSuccess(t *testing.T) {
	rt := &recordingRT{resps: []func() (*http.Response, error){
		func() (*http.Response, error) { return mkResp(503), nil },
		func() (*http.Response, error) { return mkResp(200), nil },
	}}
	tr := &RetryTransport{Config: RetryConfig{
		MaxRetries: 3, InitialDelay: 1 * time.Millisecond, MaxDelay: 5 * time.Millisecond,
	}, Next: rt}

	req, _ := http.NewRequest(http.MethodGet, "https://x/", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if rt.calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", rt.calls.Load())
	}
}

func TestRetry_AllAttemptsFail(t *testing.T) {
	rt := &recordingRT{resps: []func() (*http.Response, error){
		func() (*http.Response, error) { return mkResp(503), nil },
		func() (*http.Response, error) { return mkResp(503), nil },
		func() (*http.Response, error) { return mkResp(503), nil },
		func() (*http.Response, error) { return mkResp(503), nil },
	}}
	tr := &RetryTransport{Config: RetryConfig{
		MaxRetries: 3, InitialDelay: 1 * time.Millisecond, MaxDelay: 5 * time.Millisecond,
	}, Next: rt}

	req, _ := http.NewRequest(http.MethodGet, "https://x/", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Errorf("StatusCode = %d, want 503", resp.StatusCode)
	}
	if rt.calls.Load() != 4 { // 1 initial + 3 retries
		t.Errorf("calls = %d, want 4", rt.calls.Load())
	}
}

func TestRetry_4xxNotRetried(t *testing.T) {
	rt := &recordingRT{resps: []func() (*http.Response, error){
		func() (*http.Response, error) { return mkResp(400), nil },
	}}
	tr := &RetryTransport{Config: DefaultRetryConfig(), Next: rt}

	req, _ := http.NewRequest(http.MethodGet, "https://x/", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil || resp.StatusCode != 400 {
		t.Errorf("got %v %v, want resp=400 err=nil", resp, err)
	}
	if rt.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", rt.calls.Load())
	}
}

func TestRetry_POSTNotRetriedByDefault(t *testing.T) {
	rt := &recordingRT{resps: []func() (*http.Response, error){
		func() (*http.Response, error) { return mkResp(503), nil },
	}}
	tr := &RetryTransport{Config: DefaultRetryConfig(), Next: rt}

	req, _ := http.NewRequest(http.MethodPost, "https://x/", strings.NewReader("body"))
	resp, _ := tr.RoundTrip(req)
	if resp.StatusCode != 503 {
		t.Errorf("StatusCode = %d, want 503", resp.StatusCode)
	}
	if rt.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (POST should not retry)", rt.calls.Load())
	}
}

func TestRetry_POSTRetriedWhenAllMethodsEnabled(t *testing.T) {
	rt := &recordingRT{resps: []func() (*http.Response, error){
		func() (*http.Response, error) { return mkResp(503), nil },
		func() (*http.Response, error) { return mkResp(200), nil },
	}}
	cfg := DefaultRetryConfig()
	cfg.RetryAllMethods = true
	cfg.InitialDelay = 1 * time.Millisecond
	cfg.MaxDelay = 5 * time.Millisecond
	tr := &RetryTransport{Config: cfg, Next: rt}

	req, _ := http.NewRequest(http.MethodPost, "https://x/", strings.NewReader("body"))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("body")), nil }
	resp, _ := tr.RoundTrip(req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if rt.calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", rt.calls.Load())
	}
}

func TestRetry_HonorsRetryAfter(t *testing.T) {
	rt := &recordingRT{resps: []func() (*http.Response, error){
		func() (*http.Response, error) {
			r := mkResp(429)
			r.Header.Set("Retry-After", "1") // 1 second
			return r, nil
		},
		func() (*http.Response, error) { return mkResp(200), nil },
	}}
	tr := &RetryTransport{Config: RetryConfig{
		MaxRetries: 3, InitialDelay: 1 * time.Millisecond, MaxDelay: 5 * time.Millisecond,
	}, Next: rt}

	req, _ := http.NewRequest(http.MethodGet, "https://x/", nil)
	start := time.Now()
	resp, _ := tr.RoundTrip(req)
	elapsed := time.Since(start)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, expected >= ~1s due to Retry-After header", elapsed)
	}
}

func TestRetry_NetworkErrorRetried(t *testing.T) {
	rt := &recordingRT{resps: []func() (*http.Response, error){
		func() (*http.Response, error) { return nil, errors.New("conn reset") },
		func() (*http.Response, error) { return mkResp(200), nil },
	}}
	tr := &RetryTransport{Config: RetryConfig{
		MaxRetries: 3, InitialDelay: 1 * time.Millisecond, MaxDelay: 5 * time.Millisecond,
	}, Next: rt}

	req, _ := http.NewRequest(http.MethodGet, "https://x/", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("got %v %v, want resp=200 err=nil", resp, err)
	}
	if rt.calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", rt.calls.Load())
	}
}

func TestRetry_ContextCancellationStopsImmediately(t *testing.T) {
	rt := &recordingRT{resps: []func() (*http.Response, error){
		func() (*http.Response, error) { return nil, context.Canceled },
	}}
	tr := &RetryTransport{Config: DefaultRetryConfig(), Next: rt}

	req, _ := http.NewRequest(http.MethodGet, "https://x/", nil)
	_, err := tr.RoundTrip(req)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if rt.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (ctx canceled — no retry)", rt.calls.Load())
	}
}

// Integration test: backoff is bounded.
func TestRetry_BackoffCappedAtMaxDelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	tr := &RetryTransport{Config: RetryConfig{
		MaxRetries:   2,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
	}}
	hc := &http.Client{Transport: tr}

	start := time.Now()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, _ := hc.Do(req)
	elapsed := time.Since(start)
	defer resp.Body.Close()
	// 2 retries × max 10ms each = max 20ms backoff (plus minor overhead)
	if elapsed > 200*time.Millisecond {
		t.Errorf("elapsed = %v — backoff cap not respected", elapsed)
	}
}
