package ncloud

import (
	"errors"
	"net/http"
	"testing"
)

func mkResp(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}}
}

func TestAPIError_Skipped_For2xx(t *testing.T) {
	if err := AsAPIError(mkResp(200), []byte(`{"foo":"bar"}`)); err != nil {
		t.Errorf("2xx → AsAPIError should be nil, got %v", err)
	}
}

func TestAPIError_Skipped_ForNilResponse(t *testing.T) {
	if err := AsAPIError(nil, []byte(`{}`)); err != nil {
		t.Errorf("nil response → AsAPIError should be nil, got %v", err)
	}
}

// All 8 envelope shapes from the live audit must round-trip into typed
// fields. The body strings come straight from the smoke probe output.
func TestAPIError_EnvelopeShapes(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode string
		wantMsg  string
	}{
		{
			"shape-1 NCP responseError (autoscaling 403)",
			403,
			`{"responseError":{"returnCode":"1103","returnMessage":"Forbidden."}}`,
			"1103", "Forbidden.",
		},
		{
			"shape-2 NCP REST error.errorCode (api-gateway 404)",
			404,
			`{"error":{"errorCode":"10008","message":"tenantId [abc] not found."}}`,
			"10008", "tenantId [abc] not found.",
		},
		{
			"shape-3 top-level errorCode (sso 400)",
			400,
			`{"errorCode":9014,"message":"Tenant not created"}`,
			"9014", "Tenant not created",
		},
		{
			"shape-4 code+msg (private-ca 400)",
			400,
			`{"code":"NOT_EXIST_CASTORE","msg":"castore not found","data":null}`,
			"NOT_EXIST_CASTORE", "castore not found",
		},
		{
			"shape-5 status.code (clova-studio 401)",
			401,
			`{"status":{"code":"40100","message":"Unauthorized"}}`,
			"40100", "Unauthorized",
		},
		{
			"shape-6 Spring Boot (organization 401)",
			401,
			`{"timestamp":"2026-04-26T10:50:30Z","status":401,"error":"UNAUTHORIZED","message":"main account only","code":"COMMON_999"}`,
			"UNAUTHORIZED", "main account only",
		},
		{
			"shape-7 S3 XML (object-storage 403)",
			403,
			`<?xml version="1.0"?><Error><Code>AccessDenied</Code><Message>Access Denied</Message><RequestId>abc</RequestId></Error>`,
			"AccessDenied", "Access Denied",
		},
		{
			"shape-8 error.code (cloud-outbound-mailer 403)",
			403,
			`{"error":{"code":"77202","message":"No permission"}}`,
			"77202", "No permission",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae := AsAPIError(mkResp(tc.status), []byte(tc.body))
			if ae == nil {
				t.Fatal("AsAPIError returned nil")
			}
			if ae.StatusCode != tc.status {
				t.Errorf("StatusCode = %d, want %d", ae.StatusCode, tc.status)
			}
			if ae.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", ae.Code, tc.wantCode)
			}
			if ae.Message != tc.wantMsg {
				t.Errorf("Message = %q, want %q", ae.Message, tc.wantMsg)
			}
		})
	}
}

// errors.As must match — it's the canonical Go pattern callers will use.
func TestAPIError_ErrorsAsCompatible(t *testing.T) {
	ae := AsAPIError(mkResp(404), []byte(`{"error":{"errorCode":"42","message":"gone"}}`))
	if ae == nil {
		t.Fatal("AsAPIError returned nil")
	}
	var err error = ae
	var target *APIError
	if !errors.As(err, &target) {
		t.Fatal("errors.As did not match *APIError")
	}
	if target.Code != "42" {
		t.Errorf("Code = %q, want 42", target.Code)
	}
}

// Empty body 4xx is still a real APIError — Code/Message stay empty but
// the StatusCode and RawBody are still surfaced.
func TestAPIError_EmptyBodyStillReports(t *testing.T) {
	ae := AsAPIError(mkResp(401), []byte(""))
	if ae == nil {
		t.Fatal("AsAPIError returned nil for empty 4xx body")
	}
	if ae.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", ae.StatusCode)
	}
	if ae.Code != "" || ae.Message != "" {
		t.Errorf("expected empty Code/Message for empty body; got %q %q", ae.Code, ae.Message)
	}
	if got := ae.Error(); got != "HTTP 401 (no envelope)" {
		t.Errorf("Error() = %q, want fallback", got)
	}
}

// Request ID propagation from response headers.
func TestAPIError_RequestIDFromHeader(t *testing.T) {
	resp := mkResp(500)
	resp.Header.Set("X-Amz-Request-Id", "S3-ABC-123")
	ae := AsAPIError(resp, []byte(`<Error><Code>InternalError</Code></Error>`))
	if ae == nil || ae.RequestID != "S3-ABC-123" {
		t.Errorf("expected RequestID=S3-ABC-123, got ae=%v", ae)
	}
}
