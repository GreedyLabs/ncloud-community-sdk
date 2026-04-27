// Package cmd hosts the cobra root + global helpers shared by the
// generated per-service command trees under ../services.
//
// All subcommands share a single ncloud.Config built once in
// PersistentPreRunE; generated code retrieves it via ConfigFromCtx.
// Generated code attaches its service tree via RegisterServiceCmd.
// Output is rendered via Render(w, payload).
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	ncloud "github.com/greedylabs/ncloud-community-sdk/sdk"
	"github.com/greedylabs/ncloud-community-sdk/sdk/auth"
	"github.com/spf13/cobra"
)

// Build-time injected via -ldflags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// Global flag values.
var (
	flagRegion        string
	flagEnvironment   string
	flagProfile       string
	flagOutput        string
	flagNoRetry       bool
	flagRetryAttempts int
)

type ctxKey int

const ctxKeyConfig ctxKey = iota

var rootCmd = &cobra.Command{
	Use:   "ncc",
	Short: "Community CLI for NAVER Cloud Platform",
	Long: `ncc is the community NCP CLI built on top of ncloud-community-sdk.

It mirrors the AWS-style configuration model: credentials, region, and
environment are resolved from (in order) command-line flags, environment
variables (NCLOUD_*), the shared config file at
~/.config/ncloud-community/configure, and finally hard-coded defaults
(region kr / environment public).

Run 'ncc configure' to interactively populate the shared config file.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		switch cmd.Name() {
		case "configure", "version", "help":
			return nil
		}
		opts := []ncloud.LoadOption{}
		if flagRegion != "" {
			opts = append(opts, ncloud.WithRegion(flagRegion))
		}
		if flagEnvironment != "" {
			opts = append(opts, ncloud.WithEnvironment(ncloud.Environment(flagEnvironment)))
		}
		if flagProfile != "" {
			opts = append(opts, ncloud.WithProfile(flagProfile))
		}
		cfg, err := ncloud.LoadDefaultConfig(cmd.Context(), opts...)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		// Wire SDK retry middleware unless the user opted out.
		// Defaults to DefaultRetryConfig (3 retries, 100ms→30s with
		// jitter, 5xx + 429 only, idempotent methods only). The
		// SDK's NewHTTPClient / NewS3HTTPClient pick this up via
		// cfg.Retry and wrap RetryTransport BELOW signing — every
		// retry produces a fresh signature with a fresh timestamp,
		// so NCP doesn't reject the second attempt as a replay.
		if !flagNoRetry {
			rc := auth.DefaultRetryConfig()
			if flagRetryAttempts > 0 {
				rc.MaxRetries = flagRetryAttempts
			}
			cfg.Retry = &rc
		}
		ctx := context.WithValue(cmd.Context(), ctxKeyConfig, cfg)
		cmd.SetContext(ctx)
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagRegion, "region", "",
		"NCP region (kr|sg|jp|de|usw). Overrides $NCLOUD_REGION and shared config.")
	rootCmd.PersistentFlags().StringVar(&flagEnvironment, "environment", "",
		"NCP environment (public|financial|gov). Overrides $NCLOUD_ENVIRONMENT and shared config.")
	rootCmd.PersistentFlags().StringVar(&flagProfile, "profile", "",
		"Profile name in the shared config file. Overrides $NCLOUD_PROFILE (default DEFAULT).")
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "json",
		"Output format: json | yaml.")
	rootCmd.PersistentFlags().BoolVar(&flagNoRetry, "no-retry", false,
		"Disable automatic retry of transient failures (5xx / 429).")
	rootCmd.PersistentFlags().IntVar(&flagRetryAttempts, "retry-attempts", 0,
		"Override retry attempt count (default 3 from SDK). Ignored when --no-retry is set.")
}

// pendingServiceCmds collects per-service trees registered from
// services/<svc>.gen.go init() functions. We attach them to rootCmd
// in Execute() in deterministic alphabetical order regardless of
// init() invocation order.
var pendingServiceCmds []*cobra.Command

// RegisterServiceCmd is called from each generated services/<svc>.go
// init() to advertise its command tree to the root.
func RegisterServiceCmd(c *cobra.Command) {
	pendingServiceCmds = append(pendingServiceCmds, c)
}

// Execute runs the root command. main() delegates here so we can keep
// the entry point a one-liner.
func Execute() error {
	sort.Slice(pendingServiceCmds, func(i, j int) bool {
		return pendingServiceCmds[i].Use < pendingServiceCmds[j].Use
	})
	for _, c := range pendingServiceCmds {
		rootCmd.AddCommand(c)
	}

	err := rootCmd.ExecuteContext(context.Background())
	if err != nil && errors.Is(err, auth.ErrNoCredentials) {
		return errors.New(
			"no NCP credentials found.\n\n" +
				"The shared config file is optional — environment variables work too.\n" +
				"Set up credentials by either:\n" +
				"  - running `ncc configure` to populate ~/.config/ncloud-community/configure, OR\n" +
				"  - exporting environment variables in your shell:\n" +
				"      export NCLOUD_ACCESS_KEY_ID=<your-iam-access-key>\n" +
				"      export NCLOUD_SECRET_KEY=<your-iam-secret-key>",
		)
	}
	return err
}

// ConfigFromCtx returns the resolved ncloud.Config that
// PersistentPreRunE stashed in the cobra context. Generated code
// calls this at the top of every RunE.
func ConfigFromCtx(cmd *cobra.Command) (ncloud.Config, error) {
	v := cmd.Context().Value(ctxKeyConfig)
	if v == nil {
		return ncloud.Config{}, errors.New("no config in context (PersistentPreRunE skipped?)")
	}
	cfg, ok := v.(ncloud.Config)
	if !ok {
		return ncloud.Config{}, errors.New("context value is not ncloud.Config")
	}
	return cfg, nil
}

// OutputFormat returns the value of -o (json|yaml). Generated code
// reads this in its RunE if it wants format-aware behavior beyond
// the default Render(w, payload).
func OutputFormat() string { return flagOutput }

// printErr shows an error with a leading red "Error:" tag on
// terminals; just plain text on pipes.
func printErr(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
}
