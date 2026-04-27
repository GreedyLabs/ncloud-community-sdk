// Package auth implements NCP API Gateway HMAC v2 request signing.
//
// Every call to an NCP API is authenticated by three headers:
//
//	x-ncp-iam-access-key      the sub account's access key ID
//	x-ncp-apigw-timestamp     epoch milliseconds as a decimal string
//	x-ncp-apigw-signature-v2  HMAC-SHA256 of the canonical string, base64
//
// The canonical string is:
//
//	<HTTP method> <space> <path with query>
//	<newline>
//	<timestamp>
//	<newline>
//	<access key>
//
// Reference:
//
//	https://api.ncloud-docs.com/docs/common-ncpapi
//
// This package intentionally has no dependencies beyond the Go standard
// library so it can drop into any oapi-codegen-generated client.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Credentials carry the static or dynamically-issued NCP IAM key pair.
// SecretKey is only used locally to compute HMACs — it never leaves the
// client process.
type Credentials struct {
	AccessKey string
	SecretKey string
}

// Valid returns nil if both fields are populated.
func (c Credentials) Valid() error {
	if c.AccessKey == "" {
		return errors.New("credentials: AccessKey is empty")
	}
	if c.SecretKey == "" {
		return errors.New("credentials: SecretKey is empty")
	}
	return nil
}

// Signer produces the three HMAC v2 headers for an outbound request.
//
// Use Sign(req) as a one-liner if you construct the *http.Request yourself,
// or wrap an http.RoundTripper with Transport so that ALL requests made
// through a given http.Client are signed automatically.
type Signer struct {
	Creds Credentials
	// NowFunc is injected for tests; defaults to time.Now in UTC.
	NowFunc func() time.Time
}

// Sign populates the three headers in req.Header.
func (s Signer) Sign(req *http.Request) error {
	if err := s.Creds.Valid(); err != nil {
		return err
	}
	now := time.Now
	if s.NowFunc != nil {
		now = s.NowFunc
	}
	ts := strconv.FormatInt(now().UnixNano()/int64(time.Millisecond), 10)
	sig, err := hmacV2Signature(req.Method, pathWithQuery(req), ts, s.Creds)
	if err != nil {
		return err
	}
	req.Header.Set("x-ncp-iam-access-key", s.Creds.AccessKey)
	req.Header.Set("x-ncp-apigw-timestamp", ts)
	req.Header.Set("x-ncp-apigw-signature-v2", sig)
	return nil
}

// Transport wraps an underlying http.RoundTripper and signs every request
// with the given Credentials just before dispatch.
//
//	client := &http.Client{Transport: &auth.Transport{
//	    Creds: creds,
//	    Next:  http.DefaultTransport,
//	}}
type Transport struct {
	Creds Credentials
	Next  http.RoundTripper

	// NowFunc is injected for tests; defaults to time.Now in UTC.
	NowFunc func() time.Time
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone request to avoid mutating caller's headers (per RoundTripper contract).
	req2 := req.Clone(req.Context())
	if req2.Header == nil {
		req2.Header = make(http.Header)
	}
	s := Signer{Creds: t.Creds, NowFunc: t.NowFunc}
	if err := s.Sign(req2); err != nil {
		return nil, fmt.Errorf("hmacv2: %w", err)
	}
	next := t.Next
	if next == nil {
		next = http.DefaultTransport
	}
	return next.RoundTrip(req2)
}

// hmacV2Signature computes the base64-encoded HMAC-SHA256 signature over the
// canonical NCP string and returns it ready for the signature-v2 header.
func hmacV2Signature(method, pathWithQuery, timestamp string, creds Credentials) (string, error) {
	// Canonical format:
	//   <METHOD> <space> <path[?query]>
	//   \n
	//   <timestamp>
	//   \n
	//   <accessKey>
	var b strings.Builder
	b.Grow(len(method) + 1 + len(pathWithQuery) + 1 + len(timestamp) + 1 + len(creds.AccessKey))
	b.WriteString(strings.ToUpper(method))
	b.WriteByte(' ')
	b.WriteString(pathWithQuery)
	b.WriteByte('\n')
	b.WriteString(timestamp)
	b.WriteByte('\n')
	b.WriteString(creds.AccessKey)

	mac := hmac.New(sha256.New, []byte(creds.SecretKey))
	if _, err := mac.Write([]byte(b.String())); err != nil {
		return "", fmt.Errorf("hmac write: %w", err)
	}
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// pathWithQuery returns `req.URL.RequestURI()` but defensively handles nil.
func pathWithQuery(req *http.Request) string {
	if req == nil || req.URL == nil {
		return "/"
	}
	return req.URL.RequestURI()
}
