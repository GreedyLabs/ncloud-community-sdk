package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show ncc version, build info, and SDK module version.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		// Default values come from -ldflags at build time. When the
		// binary is `go run` / `go install`'d those are "dev"/"unknown",
		// in which case we fall back to runtime/debug.ReadBuildInfo
		// for the module version of the SDK.
		fmt.Printf("ncc  %s  (%s, built %s)\n", version, commit, date)
		fmt.Printf("go   %s\n", runtime.Version())
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, dep := range info.Deps {
				if dep.Path == "github.com/greedylabs/ncloud-community-sdk/sdk" {
					fmt.Printf("sdk  %s\n", dep.Version)
					break
				}
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
