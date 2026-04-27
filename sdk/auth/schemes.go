// Authentication schemes for NCP services that do NOT use IAM HMAC v2.
//
// NCP exposes APIs under several distinct authentication models and the
// SDK has to keep them clearly separated. A given Naver Cloud product
// almost always lives under exactly one of these:
//
//	HMAC v2          — IAM access/secret + signed canonical request.
//	                   Used by ~65 of the 77 catalogued services
//	                   (vpc, server, mysql, sub-account, wms, sso, …).
//	                   See hmacv2.go for the implementation.
//
//	APIGW Client Key — A pair of headers issued per registered app:
//	                     X-NCP-APIGW-API-KEY-ID
//	                     X-NCP-APIGW-API-KEY
//	                   Used by AI/translation products such as Papago
//	                   language-detection and Application Maps. The
//	                   keys are NOT the IAM access/secret — the user
//	                   provisions them when they register an "API Key"
//	                   in the NCP console.
//
//	Bearer           — A JWT-style token in `Authorization: Bearer`.
//	                   CLOVA Studio is the canonical example. Tokens
//	                   are issued out-of-band and expire.
//
//	Custom header    — A single per-service secret header. Examples:
//	                     X-CLOVASPEECH-API-KEY  (CLOVA Speech)
//	                     X-OCR-SECRET           (CLOVA OCR — also runs
//	                                             on a per-customer
//	                                             subdomain)
//	                     X-ARCEYE-SECRET        (Arc Eye)
//	                     x-api-key + x-project-id (NCloud Chat)
//
// To call an API that uses one of these alternate schemes, build an
// *http.Client whose Transport injects the right headers, then hand
// that client to the per-service oapi-codegen client. The Transports
// below all implement http.RoundTripper, so they slot into ncloud
// .Config.Transport just like the IAM HMAC chain does.

package auth

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
)

// ----------------------------------------------------------------------------
// APIGW Client Key — X-NCP-APIGW-API-KEY-ID + X-NCP-APIGW-API-KEY
// ----------------------------------------------------------------------------

// ClientKey is the (Client ID, Client Secret) pair NCP issues for an
// "API Key" registered against an app. It is distinct from the IAM
// access/secret used for HMAC v2 — do not mix them up.
type ClientKey struct {
	ClientID     string
	ClientSecret string
}

// Valid returns nil if both fields are populated.
func (k ClientKey) Valid() error {
	if k.ClientID == "" {
		return errors.New("client key: ClientID is empty")
	}
	if k.ClientSecret == "" {
		return errors.New("client key: ClientSecret is empty")
	}
	return nil
}

// ClientKeyProvider yields a ClientKey on demand, mirroring the
// Credentials/Provider split used by the HMAC v2 chain.
type ClientKeyProvider interface {
	ClientKey() (ClientKey, error)
}

// StaticClientKey returns a provider backed by a fixed pair.
func StaticClientKey(k ClientKey) ClientKeyProvider { return staticClientKey{k} }

type staticClientKey struct{ k ClientKey }

func (s staticClientKey) ClientKey() (ClientKey, error) {
	if err := s.k.Valid(); err != nil {
		return ClientKey{}, err
	}
	return s.k, nil
}

// EnvClientKey loads the pair from environment variables:
//
//	NCLOUD_API_KEY_ID
//	NCLOUD_API_KEY
//
// These are deliberately distinct from NCLOUD_ACCESS_KEY_ID/SECRET_KEY so
// that a single process can hold IAM creds AND an APIGW client key without
// the two stomping on each other.
type EnvClientKey struct{}

func (EnvClientKey) ClientKey() (ClientKey, error) {
	k := ClientKey{
		ClientID:     os.Getenv("NCLOUD_API_KEY_ID"),
		ClientSecret: os.Getenv("NCLOUD_API_KEY"),
	}
	if err := k.Valid(); err != nil {
		return ClientKey{}, errors.New("env: NCLOUD_API_KEY_ID/NCLOUD_API_KEY not set")
	}
	return k, nil
}

// ClientKeyTransport is an http.RoundTripper that injects the two NCP
// APIGW Client Key headers into every outbound request.
type ClientKeyTransport struct {
	Keys ClientKeyProvider
	Next http.RoundTripper
}

func (t *ClientKeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	k, err := t.Keys.ClientKey()
	if err != nil {
		return nil, fmt.Errorf("apigw client key: %w", err)
	}
	req.Header.Set("X-NCP-APIGW-API-KEY-ID", k.ClientID)
	req.Header.Set("X-NCP-APIGW-API-KEY", k.ClientSecret)
	return t.next().RoundTrip(req)
}

func (t *ClientKeyTransport) next() http.RoundTripper {
	if t.Next != nil {
		return t.Next
	}
	return http.DefaultTransport
}

// APIKeySecretOnlyTransport sends ONLY the APIGW Client Secret value, under
// a caller-chosen header name. CLOVA Speech and CLOVA OCR follow this
// pattern: their per-app secret is issued via the same NCP API Gateway "API
// Key 생성" workflow that backs ClientKey, but the resulting Primary Key
// Secret travels in a product-specific header instead of the standard
// X-NCP-APIGW-API-KEY pair:
//
//	CLOVA Speech : X-CLOVASPEECH-API-KEY : "{앱 등록 시 발급받은 Secret Key}"
//	CLOVA OCR    : X-OCR-SECRET          : "{앱 등록 시 발급받은 Secret Key}"
//
// Reusing the ClientKey provider (and therefore NCLOUD_API_KEY) means a
// caller does NOT need a separate env var per CLOVA product — the same
// APIGW-issued Secret value works for all of them.
//
// The Client ID portion of ClientKey is intentionally ignored here; these
// products identify the app by URL/subdomain.
type APIKeySecretOnlyTransport struct {
	HeaderName string             // e.g. "X-OCR-SECRET"
	Keys       ClientKeyProvider  // typically auth.EnvClientKey{} — only the Secret is read
	Next       http.RoundTripper
}

func (t *APIKeySecretOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.HeaderName == "" {
		return nil, errors.New("apikey secret-only: HeaderName is empty")
	}
	k, err := t.Keys.ClientKey()
	if err != nil {
		return nil, fmt.Errorf("apikey secret-only: %w", err)
	}
	req.Header.Set(t.HeaderName, k.ClientSecret)
	return t.next().RoundTrip(req)
}

func (t *APIKeySecretOnlyTransport) next() http.RoundTripper {
	if t.Next != nil {
		return t.Next
	}
	return http.DefaultTransport
}

// ----------------------------------------------------------------------------
// Bearer Token — Authorization: Bearer <token>
// ----------------------------------------------------------------------------

// BearerProvider yields a token. Implementations may be static,
// refresh-on-expiry, or backed by an OIDC flow.
type BearerProvider interface {
	Token() (string, error)
}

// StaticBearer returns a provider backed by a fixed token string.
func StaticBearer(token string) BearerProvider { return staticBearer{token} }

type staticBearer struct{ t string }

func (s staticBearer) Token() (string, error) {
	if s.t == "" {
		return "", errors.New("bearer: token is empty")
	}
	return s.t, nil
}

// EnvBearer reads the token from NCLOUD_BEARER_TOKEN.
type EnvBearer struct{}

func (EnvBearer) Token() (string, error) {
	t := os.Getenv("NCLOUD_BEARER_TOKEN")
	if t == "" {
		return "", errors.New("env: NCLOUD_BEARER_TOKEN not set")
	}
	return t, nil
}

// BearerTransport injects `Authorization: Bearer <token>` on every request.
// Optional `ExtraHeaders` lets callers add static side-headers some NCP
// products require alongside the bearer (e.g. CLOVA Studio's
// X-NCP-CLOVASTUDIO-REQUEST-ID).
type BearerTransport struct {
	Tokens       BearerProvider
	ExtraHeaders map[string]string
	Next         http.RoundTripper
}

func (t *BearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tok, err := t.Tokens.Token()
	if err != nil {
		return nil, fmt.Errorf("bearer: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range t.ExtraHeaders {
		req.Header.Set(k, v)
	}
	return t.next().RoundTrip(req)
}

func (t *BearerTransport) next() http.RoundTripper {
	if t.Next != nil {
		return t.Next
	}
	return http.DefaultTransport
}

// ----------------------------------------------------------------------------
// Custom single-header secret — e.g. X-OCR-SECRET, X-ARCEYE-SECRET
// ----------------------------------------------------------------------------

// HeadersProvider yields a static map of headers to inject. This is the
// catch-all for products whose auth is "set this one (or two) header(s)
// to the secret string the console gave you." NCloud Chat needs both
// `x-api-key` and `x-project-id`, hence the map shape.
type HeadersProvider interface {
	Headers() (map[string]string, error)
}

// StaticHeaders returns a provider backed by a fixed map.
func StaticHeaders(h map[string]string) HeadersProvider {
	cp := make(map[string]string, len(h))
	for k, v := range h {
		cp[k] = v
	}
	return staticHeaders{cp}
}

type staticHeaders struct{ h map[string]string }

func (s staticHeaders) Headers() (map[string]string, error) {
	if len(s.h) == 0 {
		return nil, errors.New("custom headers: empty")
	}
	return s.h, nil
}

// EnvHeaders builds a HeadersProvider from a name → env-var mapping. It
// resolves the env vars at call time and errors if any are unset, which
// keeps misconfiguration from silently producing unauthenticated requests.
//
// Example — CLOVA OCR (single header):
//
//	auth.EnvHeaders{Mapping: map[string]string{"X-OCR-SECRET": "NCLOUD_OCR_SECRET"}}
//
// Example — NCloud Chat (two headers):
//
//	auth.EnvHeaders{Mapping: map[string]string{
//	    "x-api-key":    "NCLOUD_CHAT_API_KEY",
//	    "x-project-id": "NCLOUD_CHAT_PROJECT_ID",
//	}}
//
// Conventional env-var names per service (the SDK does not enforce these
// names — they are just the recommended convention so a single .env file
// can carry credentials for many products without collision):
//
//	CLOVA Speech : NCLOUD_CLOVA_SPEECH_API_KEY     → X-CLOVASPEECH-API-KEY
//	CLOVA OCR    : NCLOUD_OCR_SECRET               → X-OCR-SECRET
//	Arc Eye      : NCLOUD_ARC_EYE_SECRET           → X-ARCEYE-SECRET
//	NCloud Chat  : NCLOUD_CHAT_API_KEY,
//	               NCLOUD_CHAT_PROJECT_ID          → x-api-key, x-project-id
type EnvHeaders struct {
	// Mapping is HEADER_NAME → ENV_VAR_NAME. All env vars must be set.
	Mapping map[string]string
}

func (e EnvHeaders) Headers() (map[string]string, error) {
	if len(e.Mapping) == 0 {
		return nil, errors.New("env headers: empty mapping")
	}
	out := make(map[string]string, len(e.Mapping))
	var missing []string
	for header, env := range e.Mapping {
		v := os.Getenv(env)
		if v == "" {
			missing = append(missing, env)
			continue
		}
		out[header] = v
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("env headers: required env vars unset: %v", missing)
	}
	return out, nil
}

// HeadersTransport sets the supplied headers on every outbound request.
// Existing values for the same header names are overwritten.
type HeadersTransport struct {
	Headers HeadersProvider
	Next    http.RoundTripper
}

func (t *HeadersTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	hdrs, err := t.Headers.Headers()
	if err != nil {
		return nil, fmt.Errorf("custom headers: %w", err)
	}
	// Set in deterministic order to make request fingerprinting predictable.
	keys := make([]string, 0, len(hdrs))
	for k := range hdrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		req.Header.Set(k, hdrs[k])
	}
	return t.next().RoundTrip(req)
}

func (t *HeadersTransport) next() http.RoundTripper {
	if t.Next != nil {
		return t.Next
	}
	return http.DefaultTransport
}
