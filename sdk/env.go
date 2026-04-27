// Package ncloud provides the foundation for NCP API Gateway clients:
// credential chain, HMAC v2 signing, and environment (민간/금융/공공) selection.
//
// oapi-codegen-generated per-service clients plug into this via the shared
// http.Client produced by NewHTTPClient.
package ncloud

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/greedylabs/ncloud-community-sdk/sdk/auth"
)

// Environment selects which NCP apex domain to target.
//
// The `x-ncloud-env` extension on each spec/services/*.yaml `servers` entry
// corresponds exactly to these values, so a generated client can pick the
// right base URL by matching on this field.
type Environment string

const (
	EnvPublic    Environment = "public"    // ntruss.com     (민간)
	EnvFinancial Environment = "financial" // fin-ntruss.com (금융)
	EnvGov       Environment = "gov"       // gov-ntruss.com (공공)
)

// Apex returns the apex DNS suffix for this environment, e.g. "ntruss.com".
func (e Environment) Apex() string {
	switch e {
	case EnvPublic:
		return "ntruss.com"
	case EnvFinancial:
		return "fin-ntruss.com"
	case EnvGov:
		return "gov-ntruss.com"
	}
	return "ntruss.com"
}

// RebaseURL rewrites a baseURL's apex (host suffix) to the target environment.
//
// Input:  https://vserver.apigw.ntruss.com/vserver/v2  + EnvGov
// Output: https://vserver.apigw.gov-ntruss.com/vserver/v2
//
// This is the pure string operation the official Go SDK uses; it does NOT
// handle region-based path variants (vnks/vses2 dual-path cases), which
// must be configured per-service.
func RebaseURL(baseURL string, env Environment) string {
	// Replace the FIRST occurrence of ".ntruss.com" (or fin/gov variants) so
	// a path like ".../ntruss.com/..." is NOT accidentally touched.
	for _, candidate := range []string{".fin-ntruss.com", ".gov-ntruss.com", ".ntruss.com"} {
		if strings.Contains(baseURL, candidate) {
			return strings.Replace(baseURL, candidate, "."+env.Apex(), 1)
		}
	}
	return baseURL
}

// Config is the user-facing settings bundle for NewHTTPClient.
//
// This SDK is scoped to the NCP HMAC v2 tier — the 67 services in
// spec/services/hmac-v2/ that share a single IAM access-key/secret pair.
// Other NCP service tiers (legacy KMS v1, Naver-branded apps with
// per-project credentials, S3-compatible Object Storage) live in sibling
// spec directories but are deliberately out of scope here, because no
// single SDK abstraction can unify their per-app credential models in a
// way that wouldn't lie about the underlying reality.
//
// If you need to call those tiers, the auth/ package still ships
// building-block RoundTrippers (auth.ClientKeyTransport,
// auth.BearerTransport, auth.HeadersTransport) — wire them into your
// own *http.Client around an oapi-codegen client built from the
// relevant spec.
type Config struct {
	// Env picks the NCP environment. Zero value is EnvPublic.
	Env Environment

	// Region picks the NCP region. Zero value is "kr".
	//
	// Most HMAC v2 services are global and ignore region — the value is
	// only consulted by per-service NewFromConfig helpers that need to
	// pick a regional endpoint (vnks, vses2, cloud-log-analytics) and by
	// the SigV4 signer for Object Storage.
	Region string

	// Creds holds the IAM credential provider for HMAC v2. Zero value is
	// auth.DefaultChain() (env → file → server role).
	Creds auth.Provider

	// Timeout applies per-request. Zero value uses 30s.
	Timeout time.Duration

	// Transport is the underlying http.RoundTripper to wrap. Zero value uses
	// http.DefaultTransport.
	Transport http.RoundTripper

	// Retry, when non-nil, wraps the auth/encoding chain in a retrying
	// http.RoundTripper. The retry layer sits BELOW signing — every
	// retry produces a fresh signature with a fresh timestamp, so NCP
	// won't reject the second attempt as a replay. Pass
	// &auth.DefaultRetryConfig() for sensible defaults (3 retries,
	// 100ms→30s exponential with jitter, 5xx + 429 codes retried for
	// idempotent methods only).
	Retry *auth.RetryConfig
}

// NewHTTPClient returns an http.Client whose every request is signed with
// NCP HMAC v2 credentials resolved through Config.Creds.
//
// Pass the returned client to an oapi-codegen client as its HTTPClient:
//
//	hc, _ := ncloud.NewHTTPClient(ncloud.Config{})
//	c, _  := wms.NewClientWithResponses(
//	    "https://wms.apigw.ntruss.com/api/v1",
//	    wms.WithHTTPClient(hc),
//	)
func NewHTTPClient(cfg Config) (*http.Client, error) {
	if cfg.Env == "" {
		cfg.Env = EnvPublic
	}
	if cfg.Region == "" {
		cfg.Region = "kr"
	}
	if cfg.Creds == nil {
		cfg.Creds = auth.DefaultChain()
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Transport == nil {
		cfg.Transport = http.DefaultTransport
	}

	creds, err := cfg.Creds.Credentials()
	if err != nil {
		// %w preserves the wrapping chain so callers can branch on
		// errors.Is(err, auth.ErrNoCredentials) etc.
		return nil, fmt.Errorf("resolving credentials: %w", err)
	}

	// Transport stack (outer → inner):
	//   ensureJSONResponse — appends responseFormatType=json on form-encoded
	//                        bodies so legacy NCP XML APIs return JSON.
	//   auth.Transport     — signs requests with HMAC v2.
	//   RetryTransport     — only present when cfg.Retry is set; retries
	//                        below signing so each attempt re-signs.
	//   cfg.Transport      — the actual network transport.
	innerNext := http.RoundTripper(cfg.Transport)
	if cfg.Retry != nil {
		innerNext = &auth.RetryTransport{Config: *cfg.Retry, Next: innerNext}
	}
	return &http.Client{
		Timeout: cfg.Timeout,
		Transport: &ensureJSONResponse{
			next: &auth.Transport{
				Creds: creds,
				Next:  innerNext,
			},
		},
	}, nil
}

// S3Config configures NewS3HTTPClient. The IAM credential chain
// (Creds field) is shared with NewHTTPClient — NCP issues one
// access-key/secret pair that signs both NCP HMAC v2 requests and AWS
// SigV4 requests against Object Storage. Region defaults to "kr"; only
// kr is currently documented for NCP Object Storage.
type S3Config struct {
	// Creds resolves the IAM access-key/secret. Zero value uses
	// auth.DefaultChain() (env → file → server role).
	Creds auth.Provider

	// Region is the NCP region. Defaults to "kr".
	Region string

	// Timeout applies per-request. Zero value uses 30s.
	Timeout time.Duration

	// Transport is the underlying http.RoundTripper to wrap. Zero value
	// uses http.DefaultTransport.
	Transport http.RoundTripper

	// Retry, when non-nil, wraps the network transport in a retrying
	// http.RoundTripper. Sits BELOW SigV4 signing so each attempt is
	// freshly signed with a fresh X-Amz-Date.
	Retry *auth.RetryConfig
}

// NewS3HTTPClient returns an http.Client that signs every outbound
// request with AWS Signature Version 4, using the resolved IAM
// credentials as the signing material. Hand the result to the
// generated `services/object_storage` client (or any other
// SigV4-protected NCP endpoint).
//
// This is a SEPARATE entry point from NewHTTPClient because the
// signing model is fundamentally different — NewHTTPClient signs with
// NCP HMAC v2 (3-line canonical, single-secret HMAC) while this
// signs with AWS SigV4 (6-line canonical request, derived per-day
// signing key, payload-hash header). Mixing them on one *http.Client
// would yield requests no NCP service would accept.
//
//	hc, _ := ncloud.NewS3HTTPClient(ncloud.S3Config{})
//	c, _  := object_storage.NewClientWithResponses(
//	    "https://kr.object.ncloudstorage.com",
//	    object_storage.WithHTTPClient(hc),
//	)
func NewS3HTTPClient(cfg S3Config) (*http.Client, error) {
	if cfg.Creds == nil {
		cfg.Creds = auth.DefaultChain()
	}
	if cfg.Region == "" {
		cfg.Region = "kr"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Transport == nil {
		cfg.Transport = http.DefaultTransport
	}
	creds, err := cfg.Creds.Credentials()
	if err != nil {
		return nil, fmt.Errorf("resolving SigV4 credentials: %w", err)
	}
	innerNext := http.RoundTripper(cfg.Transport)
	if cfg.Retry != nil {
		innerNext = &auth.RetryTransport{Config: *cfg.Retry, Next: innerNext}
	}
	return &http.Client{
		Timeout: cfg.Timeout,
		Transport: &auth.SigV4Transport{
			Signer: auth.SigV4Signer{
				Creds:   creds,
				Service: "s3",
				Region:  cfg.Region,
			},
			Next: innerNext,
		},
	}, nil
}
