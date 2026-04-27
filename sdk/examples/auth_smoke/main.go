// auth_smoke — verify HMAC v2 signing reaches NCP correctly.
//
// Behavior: makes ONE read-only call (WMS ListMonitoringScenarios). Prints
// only the HTTP status code + item count + body length. Never prints
// credentials or signed headers.
//
// Run from the SDK repo root:
//   cd examples/auth_smoke && go build . && ./auth_smoke
//
// Credential resolution order:
//   1. process env (NCLOUD_ACCESS_KEY_ID + NCLOUD_SECRET_KEY)
//   2. ../../.env (sibling of go.mod)
//   3. ~/.config/ncloud-community/configure (DEFAULT profile)
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	ncloud "github.com/greedylabs/ncloud-community-sdk/sdk"
	"github.com/greedylabs/ncloud-community-sdk/sdk/auth"
	wms "github.com/greedylabs/ncloud-community-sdk/sdk/services/wms"
)

func main() {
	loadDotenv()

	httpClient, err := ncloud.NewHTTPClient(ncloud.Config{
		Env:     ncloud.EnvPublic,
		Creds:   auth.DefaultChain(),
		Timeout: 15 * time.Second,
	})
	if err != nil {
		fail("NewHTTPClient", err)
	}

	client, err := wms.NewClientWithResponses(
		"https://wms.apigw.ntruss.com/api/v1",
		wms.WithHTTPClient(httpClient),
	)
	if err != nil {
		fail("NewClientWithResponses", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := client.ListMonitoringScenariosWithResponse(ctx, nil)
	if err != nil {
		fail("ListMonitoringScenarios", err)
	}

	fmt.Printf("HTTP %s\n", resp.Status())
	fmt.Printf("body length: %d bytes\n", len(resp.Body))

	switch {
	case resp.JSON200 != nil:
		// 200 — auth passed end-to-end. ScenarioListResponse is a top-level
		// array of scenarios; len() is the count.
		fmt.Printf("scenarios: %d\n", len(*resp.JSON200))
		fmt.Println("smoke OK — HMAC v2 signing reached NCP and returned a typed body")
	case resp.JSON4XX != nil:
		// 4xx — typed error envelope. Auth issue most likely.
		fmt.Printf("client error envelope: %+v\n", *resp.JSON4XX)
		os.Exit(2)
	case resp.JSON5XX != nil:
		fmt.Printf("server error envelope: %+v\n", *resp.JSON5XX)
		os.Exit(3)
	default:
		// Unknown shape — print only metadata, not body content (could leak)
		fmt.Println("unexpected response shape (not 200/4XX/5XX) — not printing body")
		os.Exit(4)
	}
}

func fail(stage string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", stage, err)
	os.Exit(1)
}

// loadDotenv reads ../../.env (relative to executable working dir) and sets
// any missing env var. Existing process env wins. Tolerates `KEY=value` and
// `KEY = value` (with whitespace around =). Strips surrounding quotes.
// Never logs values.
func loadDotenv() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	for _, candidate := range []string{
		filepath.Join(cwd, ".env"),
		filepath.Join(cwd, "..", ".env"),
		filepath.Join(cwd, "..", "..", ".env"),
	} {
		if applyDotenv(candidate) {
			return
		}
	}
}

func applyDotenv(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// strip optional leading "export "
		line = strings.TrimPrefix(line, "export ")
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// strip surrounding quotes
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
	return true
}
