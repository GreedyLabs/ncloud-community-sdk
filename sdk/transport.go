package ncloud

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// ensureJSONResponse is an http.RoundTripper wrapper that forces NCP's
// legacy XML APIs (the `ncloud.apigw.<env>.com/<svc>/v2/...` family —
// server, vpc, vmysql, vmssql, vcache, vmongodb, vpostgresql, vnas,
// vloadbalancer, autoscaling, vhadoop, ses, ses2, cdss, cdn — and the
// billing API) to return JSON instead of XML.
//
// NCP's legacy services accept the magic parameter `responseFormatType=json`
// to switch the response shape. Where the parameter goes depends on the
// request type:
//
//   - Form-encoded body  → appended to the body (POST /getVpcList, etc.)
//   - All other shapes   → appended to the URL query string (GET /billing/v1/...)
//
// Modern REST services that already return JSON ignore the unknown query
// param, so adding it on every non-form request is safe.
//
// This mirrors the pattern the official `ncloud-sdk-go-v2` uses internally,
// and lets oapi-codegen-generated clients (which expect application/json
// responses) work transparently against legacy NCP endpoints.
type ensureJSONResponse struct {
	next http.RoundTripper
}

func (t *ensureJSONResponse) RoundTrip(req *http.Request) (*http.Response, error) {
	ct := req.Header.Get("Content-Type")
	isForm := strings.HasPrefix(ct, "application/x-www-form-urlencoded") && req.Body != nil

	if isForm {
		// Read existing body and append responseFormatType=json if missing.
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()

		bodyStr := string(body)
		if !strings.Contains(bodyStr, "responseFormatType=") {
			if bodyStr != "" {
				bodyStr += "&"
			}
			bodyStr += "responseFormatType=json"
		}

		newBody := []byte(bodyStr)
		req.Body = io.NopCloser(bytes.NewReader(newBody))
		req.ContentLength = int64(len(newBody))
		// GetBody enables transparent retries / redirect-follow with the
		// same body.
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(newBody)), nil
		}
	} else if req.URL != nil {
		// Non-form request: stamp the query string instead. Legacy services
		// (billing, etc.) consult it; modern REST services ignore it.
		q := req.URL.Query()
		if q.Get("responseFormatType") == "" {
			q.Set("responseFormatType", "json")
			req.URL.RawQuery = q.Encode()
		}
	}

	resp, err := t.next.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		return resp, nil
	}
	resp.Body = unwrapResponseEnvelope(resp.Body)
	resp.ContentLength = -1 // length is now unknown
	return resp, nil
}

// unwrapResponseEnvelope strips NCP's legacy single-key response wrapper
// (e.g. `{ "getVpcListResponse": { ... } }` → `{ ... }`).
//
// NCP's legacy form-encoded APIs return JSON with one outer object whose
// sole key is the operation name + "Response". oapi-codegen-generated
// clients expect the inner object directly; unwrap so they Just Work.
//
// If the body is not a single-key envelope, return it unchanged.
func unwrapResponseEnvelope(body io.ReadCloser) io.ReadCloser {
	defer body.Close()
	raw, err := io.ReadAll(body)
	if err != nil || len(raw) == 0 {
		return io.NopCloser(bytes.NewReader(raw))
	}
	// Lightweight peek: only attempt unwrap when the JSON has exactly one
	// top-level key matching the heuristic.
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || len(envelope) != 1 {
		return io.NopCloser(bytes.NewReader(raw))
	}
	var key string
	for k := range envelope {
		key = k
	}
	if !strings.HasSuffix(key, "Response") {
		return io.NopCloser(bytes.NewReader(raw))
	}
	return io.NopCloser(bytes.NewReader(envelope[key]))
}
