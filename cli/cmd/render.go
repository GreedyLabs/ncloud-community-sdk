package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// Render serialises a typed payload (anything from the SDK's typed
// response — usually resp.JSON200 or similar) into the format the
// user picked via --output. Generated per-op RunE bodies call this
// at the end of a successful API call.
//
// Supported formats:
//   json (default) — encoding/json with sort + 2-space indent
//   yaml          — gopkg.in/yaml.v3 with 2-space indent
//
// Anything else falls back to JSON. Per-service table renderers can
// be layered on later by switching on OutputFormat() before calling
// Render.
func Render(w io.Writer, payload any) error {
	if payload == nil {
		return nil
	}
	switch strings.ToLower(flagOutput) {
	case "yaml":
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		defer enc.Close()
		return enc.Encode(payload)
	case "json", "":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	default:
		return fmt.Errorf("unknown --output value %q (want json|yaml)", flagOutput)
	}
}
