// AWS Signature Version 4 signer for NCP Object Storage.
//
// NCP Object Storage is wire-compatible with Amazon S3 — including the
// auth scheme. The same IAM access-key/secret pair issued in the NCP
// console serves as the SigV4 signing material; only the signing flow
// differs from NCP HMAC v2:
//
//   - NCP HMAC v2: 3-line canonical string (method+path, timestamp,
//                  access-key) signed with the raw secret key.
//   - AWS SigV4:   6-line canonical request (method, URI, query,
//                  headers, signed-headers, payload-hash), wrapped in
//                  a string-to-sign, signed with a per-day/region/
//                  service derived key.
//
// We implement SigV4 by hand to keep dependency surface minimal — the
// official aws-sdk-go-v2 signer would pull in 4+ AWS modules just for
// the credential type. Verified against the AWS canonical examples and
// against NCP Object Storage live (see examples/smoke_all probeObjectStorage).
//
// Reference: https://docs.aws.amazon.com/general/latest/gr/sigv4_signing.html
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// SigV4Signer produces the AWS-compatible request signature for one
// outbound request.
type SigV4Signer struct {
	Creds   Credentials // reuses the IAM access-key/secret
	Service string      // canonical service identifier — "s3" for Object Storage
	Region  string      // NCP region, e.g. "kr"

	// NowFunc is exposed for deterministic testing. Production callers
	// leave it nil and we use time.Now().UTC().
	NowFunc func() time.Time
}

// emptyPayloadHash is sha256("") — the canonical hash for requests with
// no body, which covers every read-only Object Storage call.
const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// UnsignedPayload is the literal value AWS reserves for the
// `X-Amz-Content-Sha256` header when the caller wants to skip body
// hashing. The request is still signed with SigV4 (so the headers, URL,
// and credentials are tamper-evident), but the body itself is not part
// of the signature — TLS provides the integrity guarantee instead.
//
// This is the practical answer to "I want to PUT a 50 GB file without
// reading it into memory or signing each chunk." AWS's own Go SDK uses
// this as the default for S3 PutObject. Verified live against NCP
// Object Storage 2026-04-26.
const UnsignedPayload = "UNSIGNED-PAYLOAD"

// UnsignedPayloadEditor is an oapi-codegen RequestEditorFn that pre-sets
// the X-Amz-Content-Sha256 header to UNSIGNED-PAYLOAD. Pass it as the
// last argument to any S3 PutObject call to opt into streaming uploads:
//
//	c.PutObjectWithBodyWithResponse(ctx, bucket, key, params,
//	    "application/octet-stream", veryLargeReader,
//	    auth.UnsignedPayloadEditor)
//
// SigV4Signer.Sign honours a pre-set header value rather than reading
// the body to compute the hash, so the body is streamed straight to the
// wire without buffering.
func UnsignedPayloadEditor(_ context.Context, req *http.Request) error {
	req.Header.Set("X-Amz-Content-Sha256", UnsignedPayload)
	return nil
}

// Sign annotates req with X-Amz-Date, X-Amz-Content-Sha256, and the SigV4
// Authorization header. The body is consumed to compute the payload hash
// and reset via req.GetBody() so the underlying transport can re-read it.
//
// For requests with no body (GET/HEAD/DELETE typical) the well-known
// sha256("") constant is used and req.Body is left alone.
func (s SigV4Signer) Sign(req *http.Request) error {
	if err := s.Creds.Valid(); err != nil {
		return err
	}
	if s.Service == "" {
		s.Service = "s3"
	}
	if s.Region == "" {
		s.Region = "kr"
	}
	now := s.now()
	amzDate := now.Format("20060102T150405Z")
	scopeDate := now.Format("20060102")

	// --- 1. Payload hash ----------------------------------------------------
	// Honour any pre-set X-Amz-Content-Sha256 header — the caller may have
	// asked for streaming via UNSIGNED-PAYLOAD or supplied a known SHA-256
	// to avoid the read-then-restore trip through s.payloadHash. Anything
	// else triggers the default body-buffering path.
	var payloadHash string
	if pre := req.Header.Get("X-Amz-Content-Sha256"); pre != "" {
		payloadHash = pre
	} else {
		var err error
		payloadHash, err = s.payloadHash(req)
		if err != nil {
			return fmt.Errorf("sigv4: hashing payload: %w", err)
		}
	}

	// --- 2. Required headers (must be set BEFORE building canonical) -------
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if req.Header.Get("Host") == "" && req.Host == "" {
		// net/http injects Host from req.URL when serializing, but the
		// canonical request needs it now. Set it explicitly.
		req.Header.Set("Host", req.URL.Host)
	}

	// --- 3. Canonical request -----------------------------------------------
	canonicalURI := canonicalURIPath(req.URL)
	canonicalQuery := canonicalQueryString(req.URL.Query())
	canonicalHeaders, signedHeaders := canonicalHeadersAndSignedList(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// --- 4. String to sign --------------------------------------------------
	scope := fmt.Sprintf("%s/%s/%s/aws4_request", scopeDate, s.Region, s.Service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hashHex(canonicalRequest),
	}, "\n")

	// --- 5. Signing key (kSecret → kDate → kRegion → kService → kSigning) --
	kDate := hmacSHA256([]byte("AWS4"+s.Creds.SecretKey), scopeDate)
	kRegion := hmacSHA256(kDate, s.Region)
	kService := hmacSHA256(kRegion, s.Service)
	kSigning := hmacSHA256(kService, "aws4_request")

	// --- 6. Signature & Authorization header --------------------------------
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
	auth := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.Creds.AccessKey, scope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", auth)
	return nil
}

func (s SigV4Signer) now() time.Time {
	if s.NowFunc != nil {
		return s.NowFunc().UTC()
	}
	return time.Now().UTC()
}

// payloadHash returns the hex-encoded sha256 of the request body. For
// requests with no body or empty body it returns the well-known constant
// for sha256(""). The body is fully read and replaced via GetBody() so the
// transport can re-read it.
func (s SigV4Signer) payloadHash(req *http.Request) (string, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return emptyPayloadHash, nil
	}
	buf, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return "", err
	}
	if len(buf) == 0 {
		req.Body = http.NoBody
		return emptyPayloadHash, nil
	}
	// Restore body for the transport. Use a closure-backed GetBody so
	// retries / redirects can re-read the same bytes.
	clone := append([]byte(nil), buf...)
	req.Body = io.NopCloser(strings.NewReader(string(clone)))
	req.ContentLength = int64(len(clone))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(string(clone))), nil
	}
	return hashHex(string(buf)), nil
}

// canonicalURIPath returns the percent-encoded path. AWS SigV4 requires
// the path to be normalized (no `..`/`.`) and segments to be encoded per
// RFC 3986 — which net/url's EscapedPath gives us, except for `/` which
// must remain unescaped between segments.
func canonicalURIPath(u *url.URL) string {
	if u.Path == "" {
		return "/"
	}
	// EscapedPath preserves the unescaped slashes between segments and
	// percent-encodes the unsafe bytes within each segment, which matches
	// AWS's canonicalisation. AWS also wants `/` for the root, not "".
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	return p
}

// canonicalQueryString sorts the query parameters by name (then by
// encoded value), then joins them with `&` after encoding each
// name=value pair per RFC 3986.
func canonicalQueryString(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		ek := url.QueryEscape(k)
		for _, v := range vals {
			parts = append(parts, ek+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// canonicalHeadersAndSignedList returns the canonical-headers block and
// the semicolon-joined list of signed header names. Both are derived
// from req.Header — every header except Authorization is included, since
// SigV4 signs every header it sees and the server validates the same set.
func canonicalHeadersAndSignedList(req *http.Request) (string, string) {
	type kv struct{ name, value string }
	var rows []kv
	for name, vs := range req.Header {
		l := strings.ToLower(name)
		if l == "authorization" {
			continue
		}
		// Each header value is trimmed and runs of internal whitespace
		// collapsed to a single space (per AWS spec).
		joined := strings.Join(trimAll(vs), ",")
		rows = append(rows, kv{l, collapseSpaces(joined)})
	}
	// Host must always be present in the canonical headers — http.Request
	// usually keeps it on req.Host rather than req.Header.
	if req.Header.Get("Host") == "" {
		rows = append(rows, kv{"host", req.URL.Host})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	var headersBuf strings.Builder
	var signedNames []string
	for _, r := range rows {
		headersBuf.WriteString(r.name)
		headersBuf.WriteString(":")
		headersBuf.WriteString(r.value)
		headersBuf.WriteString("\n")
		signedNames = append(signedNames, r.name)
	}
	return headersBuf.String(), strings.Join(signedNames, ";")
}

func trimAll(vs []string) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = strings.TrimSpace(v)
	}
	return out
}

func collapseSpaces(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return b.String()
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}

// SigV4Transport is the http.RoundTripper that signs every outbound
// request with AWS SigV4 before dispatching through Next.
type SigV4Transport struct {
	Signer SigV4Signer
	Next   http.RoundTripper
}

func (t *SigV4Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Next == nil {
		t.Next = http.DefaultTransport
	}
	if err := t.Signer.Sign(req); err != nil {
		return nil, err
	}
	return t.Next.RoundTrip(req)
}

// SigV4ProviderTransport is a provider-aware variant of SigV4Transport
// that resolves the IAM credentials lazily, mirroring the HMAC v2
// Transport's relationship to auth.Provider.
type SigV4ProviderTransport struct {
	Creds   Provider
	Service string
	Region  string
	Next    http.RoundTripper
}

func (t *SigV4ProviderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Creds == nil {
		return nil, errors.New("sigv4: nil Provider")
	}
	c, err := t.Creds.Credentials()
	if err != nil {
		return nil, fmt.Errorf("sigv4: %w", err)
	}
	signer := SigV4Signer{Creds: c, Service: t.Service, Region: t.Region}
	if t.Next == nil {
		t.Next = http.DefaultTransport
	}
	if err := signer.Sign(req); err != nil {
		return nil, err
	}
	return t.Next.RoundTrip(req)
}
