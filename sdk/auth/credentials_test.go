package auth

import (
	"errors"
	"strings"
	"testing"
)

// failingProvider is a test double — always returns the configured error.
type failingProvider struct{ err error }

func (f failingProvider) Credentials() (Credentials, error) { return Credentials{}, f.err }

// staticOK is a test double — always returns valid creds.
type staticOK struct{}

func (staticOK) Credentials() (Credentials, error) {
	return Credentials{AccessKey: "AK", SecretKey: "SK"}, nil
}

// First successful provider short-circuits; later providers are not consulted.
func TestChainProvider_FirstSuccessWins(t *testing.T) {
	chain := ChainProvider{Providers: []Provider{
		failingProvider{errors.New("env: not set")},
		staticOK{},
		failingProvider{errors.New("should never be called")},
	}}
	got, err := chain.Credentials()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got.AccessKey != "AK" {
		t.Errorf("AccessKey = %q, want AK", got.AccessKey)
	}
}

// All-fail returns ErrNoCredentials wrapping the joined sub-errors so
// errors.Is(err, ErrNoCredentials) works AND each sub-error is still
// reachable through the chain (errors.Is on the inner errors).
func TestChainProvider_AllFail_WrapsErrNoCredentials(t *testing.T) {
	envErr := errors.New("env: NCLOUD_ACCESS_KEY_ID/NCLOUD_SECRET_KEY not set")
	fileErr := errors.New("file: open ~/.config/ncloud-community/configure: no such file or directory")
	chain := ChainProvider{Providers: []Provider{
		failingProvider{envErr},
		failingProvider{fileErr},
	}}

	_, err := chain.Credentials()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNoCredentials) {
		t.Errorf("expected errors.Is(err, ErrNoCredentials) to be true; got: %v", err)
	}
	if !errors.Is(err, envErr) {
		t.Errorf("expected env error to be unwrappable; got: %v", err)
	}
	if !errors.Is(err, fileErr) {
		t.Errorf("expected file error to be unwrappable; got: %v", err)
	}
	// The message should mention the sentinel + at least one sub-detail
	// so users reading the surface message see what happened.
	msg := err.Error()
	if !strings.Contains(msg, "no credentials available") {
		t.Errorf("error message missing sentinel text: %q", msg)
	}
	if !strings.Contains(msg, "NCLOUD_ACCESS_KEY_ID") {
		t.Errorf("error message missing env-provider detail: %q", msg)
	}
}

// Empty chain (no providers configured) is a distinct case from
// "providers configured but all rejected" — surfaces the sentinel with
// a clear "no providers" reason.
func TestChainProvider_EmptyChain(t *testing.T) {
	chain := ChainProvider{Providers: nil}
	_, err := chain.Credentials()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNoCredentials) {
		t.Errorf("expected errors.Is(err, ErrNoCredentials) to be true; got: %v", err)
	}
	if !strings.Contains(err.Error(), "no providers") {
		t.Errorf("expected 'no providers' in message: %q", err.Error())
	}
}
