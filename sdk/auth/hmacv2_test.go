package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Known-good vector: signature for a fixed method/path/timestamp/creds pair.
// We cannot test against a real NCP endpoint here, but by pinning time and
// keys we can detect regressions in the canonical string or the HMAC pipeline.
func TestSign_StaticVector(t *testing.T) {
	creds := Credentials{AccessKey: "AK_TEST", SecretKey: "SK_TEST"}
	frozen := time.Unix(1_700_000_000, 0).UTC() // ms = 1700000000000

	req, _ := http.NewRequest(http.MethodPost,
		"https://vserver.apigw.ntruss.com/vserver/v2/createServerInstances?regionCode=KR", nil)

	s := Signer{Creds: creds, NowFunc: func() time.Time { return frozen }}
	if err := s.Sign(req); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if got := req.Header.Get("x-ncp-iam-access-key"); got != "AK_TEST" {
		t.Errorf("access-key header = %q, want AK_TEST", got)
	}
	if got := req.Header.Get("x-ncp-apigw-timestamp"); got != "1700000000000" {
		t.Errorf("timestamp = %q, want 1700000000000", got)
	}
	// Signature is deterministic for the inputs above. Recompute and compare
	// to the known value to catch any change in canonicalisation.
	want := "xBcE26XFU55BMjppdPT7ZY3oDrqoOedfPyAn3Cq4lUw="
	if got := req.Header.Get("x-ncp-apigw-signature-v2"); got != want {
		t.Errorf("signature = %q, want %q", got, want)
	}
}

func TestSign_EmptyCreds(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	s := Signer{}
	if err := s.Sign(req); err == nil {
		t.Fatal("expected error for empty credentials")
	}
}

// Transport wraps the signer and dispatches through an underlying RoundTripper.
// We verify the three headers land on the server-side request.
func TestTransport_SignsEveryRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range []string{"x-ncp-iam-access-key", "x-ncp-apigw-timestamp", "x-ncp-apigw-signature-v2"} {
			if r.Header.Get(h) == "" {
				t.Errorf("missing header %s", h)
			}
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := &http.Client{Transport: &Transport{
		Creds: Credentials{AccessKey: "AK", SecretKey: "SK"},
	}}
	resp, err := client.Get(srv.URL + "/ping")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
