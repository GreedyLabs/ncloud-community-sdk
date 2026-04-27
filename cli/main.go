// Command ncc is the community NCP CLI — an alternative to the
// official `ncloud` CLI with broader service coverage, generated from
// ncloud-community-spec.
//
// All subcommands share the same auth/region/environment loader as
// the underlying SDK (LoadDefaultConfig), so the credentials in
// $XDG_CONFIG_HOME/ncloud-community/configure work identically.
package main

import (
	"fmt"
	"os"

	"github.com/greedylabs/ncloud-community-sdk/cli/cmd"
	// Register every generated service. The blank import triggers each
	// services/<svc>.gen.go init() which calls cmd.RegisterService.
	_ "github.com/greedylabs/ncloud-community-sdk/cli/services"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
