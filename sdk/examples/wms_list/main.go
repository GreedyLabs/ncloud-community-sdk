// Example: list WMS monitoring scenarios.
//
// Build:
//   cd examples/wms_list && go build .
//
// Run (requires NCP credentials in env):
//   NCLOUD_ACCESS_KEY_ID=AK... NCLOUD_SECRET_KEY=SK... ./wms_list
//
// Or with the XDG shared config file
// (~/.config/ncloud-community/configure):
//   [DEFAULT]
//   ncloud_access_key_id     = AK...
//   ncloud_secret_access_key = SK...
//   ncloud_region            = kr
//   ncloud_environment       = public
//
// We intentionally don't read the official SDK's ~/.ncloud/configure
// path — the two SDKs use slightly different keys and we don't want
// to fight over the same file. Set WithSharedConfigPath if you want
// to share one.
package main

import (
	"context"
	"fmt"
	"os"

	ncloud "github.com/greedylabs/ncloud-community-sdk/sdk"
	wms "github.com/greedylabs/ncloud-community-sdk/sdk/services/wms"
)

func main() {
	ctx := context.Background()

	// 1. Resolve region + environment + credentials in one shot.
	//    LoadDefaultConfig walks: programmatic options > env vars >
	//    XDG config file > defaults (kr / public).
	cfg, err := ncloud.LoadDefaultConfig(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	// 2. Per-service NewFromConfig builds the signed http.Client and
	//    picks the right base URL for cfg.Env from the spec-declared
	//    ServerEndpoints map.
	client, err := wms.NewFromConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wms client: %v\n", err)
		os.Exit(1)
	}

	resp, err := client.ListMonitoringScenariosWithResponse(ctx, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("HTTP %s\n", resp.Status())
	if resp.JSON200 != nil {
		fmt.Printf("scenarios: %d\n", len(*resp.JSON200))
	}
}
