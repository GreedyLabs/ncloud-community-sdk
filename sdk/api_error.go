// APIError — typed access to NCP error envelopes.
//
// oapi-codegen returns 4xx/5xx responses as a populated *http.Response
// with non-nil err == nil — callers have to inspect StatusCode and
// re-parse the body themselves to surface the NCP error code, message,
// or request ID. This file provides a single helper that does that
// parse once per call site, matching the seven envelope variants we've
// observed in the live NCP catalog.
//
// Usage:
//
//	resp, err := client.ListXxxWithResponse(ctx, ...)
//	if err != nil { return err } // network error
//	if apiErr := ncloud.AsAPIError(resp.HTTPResponse, resp.Body); apiErr != nil {
//	    log.Printf("NCP %s: %s (request %s)", apiErr.Code, apiErr.Message, apiErr.RequestID)
//	    return apiErr
//	}
//	// resp.JSON200 is safe to use here.
//
// APIError implements the standard `error` interface and is safely
// matched by `errors.As`:
//
//	var apiErr *ncloud.APIError
//	if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
//	    // resource doesn't exist
//	}

package ncloud

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

// APIError carries the most useful fields from an NCP error envelope
// alongside the raw response context for callers who need more.
type APIError struct {
	// StatusCode is the HTTP status (4xx or 5xx — never 2xx).
	StatusCode int

	// Code is the product-specific error code. NCP uses a small handful
	// of names across products: errorCode, returnCode, code; the parser
	// maps any of them to this field.
	Code string

	// Message is the human-readable error description.
	Message string

	// Details is any extra free-form text the envelope carries (CLOVA
	// products in particular use this).
	Details string

	// RequestID is the NCP-assigned request ID, useful when contacting
	// support. May be empty if the envelope didn't carry one.
	RequestID string

	// RawBody is the response body bytes the parser consumed. Inspect
	// this when the typed fields don't have what you need.
	RawBody []byte

	// RawResponse is the underlying *http.Response. May be nil when the
	// caller passed only a body. Headers like X-Amz-Id-2 (S3-style
	// debug ID) live here.
	RawResponse *http.Response
}

// Error implements the error interface.
//
// We intentionally do NOT prefix with the package name — library
// consumers identify the error type via errors.As(err, &apiErr) or
// errors.Is, and the prefix is just visual noise for end users
// (CLI / Terraform diagnostics). Format mirrors the standard NCP
// docs convention: "HTTP <status> <code>: <message>".
func (e *APIError) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("HTTP %d %s: %s", e.StatusCode, e.Code, e.Message)
	case e.Message != "":
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	case e.Code != "":
		return fmt.Sprintf("HTTP %d %s", e.StatusCode, e.Code)
	default:
		return fmt.Sprintf("HTTP %d (no envelope)", e.StatusCode)
	}
}

// AsAPIError returns an *APIError when resp carries a 4xx/5xx status
// AND the body parses as one of the recognised NCP/S3 envelope shapes.
// Returns nil otherwise — including for 2xx responses, where there is
// no error to surface.
//
// Pass the raw body separately because oapi-codegen consumes
// resp.Body during decoding; callers typically have it on
// `resp.Body []byte` already.
func AsAPIError(resp *http.Response, body []byte) *APIError {
	if resp == nil {
		return nil
	}
	if resp.StatusCode < 400 || resp.StatusCode >= 600 {
		return nil
	}
	out := &APIError{
		StatusCode:  resp.StatusCode,
		RawBody:     body,
		RawResponse: resp,
	}
	if id := resp.Header.Get("x-ncp-request-id"); id != "" {
		out.RequestID = id
	} else if id := resp.Header.Get("X-Amz-Request-Id"); id != "" {
		out.RequestID = id
	}
	parseEnvelope(body, out)
	return out
}

// parseEnvelope walks the seven envelope shapes we've documented in the
// live audit and fills out's typed fields. If none match, the typed
// fields stay empty and the caller still has RawBody to inspect.
//
// Recognised shapes (live-verified):
//
//   1. NCP standard JSON:    {"responseError":{"returnCode":"…","returnMessage":"…"}}
//   2. NCP REST JSON:        {"error":{"errorCode":"…","message":"…","details":"…"}}
//   3. NCP REST top-level:   {"errorCode":9014,"message":"Tenant not created"}
//   4. NCP code+msg envelope: {"code":"NOT_EXIST_CASTORE","msg":"…","data":null}
//   5. CLOVA Studio:          {"status":{"code":"40100","message":"Unauthorized"}}
//   6. Spring Boot 401:       {"timestamp":"…","status":401,"error":"…","message":"…","code":"…"}
//   7. S3 XML:                <Error><Code>AccessDenied</Code><Message>…</Message><RequestId>…</RequestId></Error>
//   8. CLOVA OCR-style:       {"error":{"code":"77202","message":"No permission"}}  (similar to 2 but `code` not `errorCode`)
func parseEnvelope(body []byte, out *APIError) {
	trimmed := trimSpace(body)
	if len(trimmed) == 0 {
		return
	}
	switch trimmed[0] {
	case '<':
		parseS3XML(body, out)
	case '{':
		parseJSONEnvelope(body, out)
	}
}

// parseS3XML handles the S3 wire-format <Error> envelope.
func parseS3XML(body []byte, out *APIError) {
	var e struct {
		XMLName    xml.Name `xml:"Error"`
		Code       string   `xml:"Code"`
		Message    string   `xml:"Message"`
		Resource   string   `xml:"Resource"`
		BucketName string   `xml:"BucketName"`
		RequestId  string   `xml:"RequestId"`
		HostId     string   `xml:"HostId"`
	}
	if err := xml.Unmarshal(body, &e); err != nil {
		return
	}
	out.Code = e.Code
	out.Message = e.Message
	if e.RequestId != "" {
		out.RequestID = e.RequestId
	}
	// Use Resource/BucketName as Details when present.
	if e.Resource != "" || e.BucketName != "" {
		var detail strings.Builder
		if e.Resource != "" {
			detail.WriteString("resource=")
			detail.WriteString(e.Resource)
		}
		if e.BucketName != "" {
			if detail.Len() > 0 {
				detail.WriteString(" ")
			}
			detail.WriteString("bucket=")
			detail.WriteString(e.BucketName)
		}
		out.Details = detail.String()
	}
}

// parseJSONEnvelope walks the JSON shapes in priority order — the most
// specific shapes first so that an envelope which structurally matches
// two of them (Spring Boot in particular looks like both shape 6 and
// shape 2's "error-as-string" fallback) gets routed to the right one.
func parseJSONEnvelope(body []byte, out *APIError) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return
	}

	// Shape 6 (Spring Boot, MOST SPECIFIC) — distinguished by the
	// presence of both `timestamp` and `status` plus a string-typed
	// `error` AND a sibling `message`. Run this before shape 2 so the
	// "error-as-string" fallback there doesn't grab `error` first.
	if _, hasTs := raw["timestamp"]; hasTs {
		if statusRaw, hasStatus := raw["status"]; hasStatus && len(statusRaw) > 0 && statusRaw[0] != '{' {
			// status is a number, not an object → Spring shape.
			var errStr string
			if v, ok := raw["error"]; ok {
				_ = json.Unmarshal(v, &errStr)
			}
			var msg string
			if v, ok := raw["message"]; ok {
				_ = json.Unmarshal(v, &msg)
			}
			if errStr != "" || msg != "" {
				out.Code = errStr
				out.Message = msg
				return
			}
		}
	}

	// Shape 1: {"responseError": {...}}
	if v, ok := raw["responseError"]; ok {
		var inner struct {
			ReturnCode    string `json:"returnCode"`
			ReturnMessage string `json:"returnMessage"`
		}
		if json.Unmarshal(v, &inner) == nil {
			out.Code = inner.ReturnCode
			out.Message = inner.ReturnMessage
		}
		if out.Code != "" || out.Message != "" {
			return
		}
	}

	// Shape 2 + 8: {"error": {"errorCode" or "code": …, "message": …, "details": …}}
	if v, ok := raw["error"]; ok {
		var inner struct {
			ErrorCode json.RawMessage `json:"errorCode"`
			Code      json.RawMessage `json:"code"`
			Message   string          `json:"message"`
			Details   string          `json:"details"`
		}
		if json.Unmarshal(v, &inner) == nil {
			out.Code = pickStringOrNumber(inner.ErrorCode, inner.Code)
			out.Message = inner.Message
			out.Details = inner.Details
		}
		if out.Code != "" || out.Message != "" {
			return
		}
		// Plain-string "error" (cert-manager 404 etc.) — only reach here
		// when no Spring sibling fields suggested otherwise.
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			out.Message = s
			return
		}
	}

	// Shape 3: top-level {"errorCode": …, "message": …}
	if v, ok := raw["errorCode"]; ok {
		out.Code = decodeStringOrNumber(v)
	}
	if v, ok := raw["message"]; ok {
		_ = json.Unmarshal(v, &out.Message)
	}
	if out.Code != "" || out.Message != "" {
		return
	}

	// Shape 4: {"code": …, "msg": …, "data": …}
	if v, ok := raw["code"]; ok {
		out.Code = decodeStringOrNumber(v)
	}
	if v, ok := raw["msg"]; ok {
		_ = json.Unmarshal(v, &out.Message)
	}
	if out.Code != "" || out.Message != "" {
		return
	}

	// Shape 5: {"status": {"code": …, "message": …}}
	if v, ok := raw["status"]; ok {
		var inner struct {
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
		}
		if json.Unmarshal(v, &inner) == nil {
			out.Code = decodeStringOrNumber(inner.Code)
			out.Message = inner.Message
		}
		if out.Code != "" || out.Message != "" {
			return
		}
	}
}

// decodeStringOrNumber extracts a JSON value as a string regardless of
// whether the wire form was `"123"` or `123`.
func decodeStringOrNumber(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	if v[0] == '"' {
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
		return ""
	}
	// Numeric or bare token — return as-is, trimming whitespace.
	return strings.TrimSpace(string(v))
}

// pickStringOrNumber returns the first non-empty decode among several
// raw json messages.
func pickStringOrNumber(vs ...json.RawMessage) string {
	for _, v := range vs {
		if s := decodeStringOrNumber(v); s != "" {
			return s
		}
	}
	return ""
}

// trimSpace drops ASCII whitespace from both ends of a byte slice
// without allocating a new string.
func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\n' || b[0] == '\r') {
		b = b[1:]
	}
	for len(b) > 0 {
		last := b[len(b)-1]
		if last != ' ' && last != '\t' && last != '\n' && last != '\r' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}
