// Shared helpers used by every generated services/<svc>.gen.go.
// Hand-maintained — the codegen relies on these names + signatures.
// Lives outside the *.gen.go pattern so the regen step doesn't wipe it.
package services

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func mustAtoi(s string) int           { n, _ := strconv.Atoi(s); return n }
func mustAtoi64(s string) int64       { n, _ := strconv.ParseInt(s, 10, 64); return n }
func mustParseBool(s string) bool     { b, _ := strconv.ParseBool(s); return b }
func mustParseFloat(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }

// mustParseUUID converts a CLI string flag into the SDK's UUID type.
// runtime/types.UUID is a type alias for google/uuid.UUID, so we
// parse via uuid.Parse and cast.
func mustParseUUID(s string) openapi_types.UUID {
	u, _ := uuid.Parse(s)
	return openapi_types.UUID(u)
}

// pickPayload returns the first non-nil JSON2xx field on the SDK's
// typed HTTPResp via reflection. resp.JSON200 is the usual case but
// some ops have JSON201 / JSON204 / etc — reflection keeps the
// generated dispatchers free of per-op casework.
func pickPayload(resp any) any {
	rv := reflect.ValueOf(resp)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return resp
	}
	for i := 0; i < rv.NumField(); i++ {
		name := rv.Type().Field(i).Name
		if !strings.HasPrefix(name, "JSON") {
			continue
		}
		fv := rv.Field(i)
		if fv.IsValid() && !fv.IsZero() {
			return fv.Interface()
		}
	}
	return nil
}
