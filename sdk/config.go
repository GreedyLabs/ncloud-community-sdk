// Package ncloud — LoadDefaultConfig and the precedence chain.
//
// Mirrors the AWS SDK v2 model: a single `Config` value carries
// region/environment/credentials, and per-service `NewFromConfig`
// helpers (generated under services/.../endpoints.gen.go) consume it.
//
// Precedence, highest → lowest:
//
//  1. Programmatic overrides via WithRegion / WithEnvironment /
//     WithCredentialsProvider passed to LoadDefaultConfig.
//  2. Environment variables:
//     NCLOUD_REGION, NCLOUD_ENVIRONMENT,
//     NCLOUD_ACCESS_KEY_ID + NCLOUD_SECRET_KEY.
//  3. Shared config file:
//     $XDG_CONFIG_HOME/ncloud-community/configure
//     → $HOME/.config/ncloud-community/configure
//     selected profile (default DEFAULT, or $NCLOUD_PROFILE).
//     $HOME/.ncloud/configure (the official NCP SDK's path) is
//     intentionally NOT searched — pass WithSharedConfigPath if you
//     want to point this loader at it.
//  4. Hard-coded defaults:
//     Region "kr", Environment EnvPublic.
//
// Credentials follow the same precedence but use the existing
// auth.DefaultChain() (env → file → server role) when none is supplied
// programmatically. The chain is preserved verbatim so existing callers
// that only set Creds keep working.
package ncloud

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/greedylabs/ncloud-community-sdk/sdk/auth"
)

// loadState holds the resolution-time inputs that don't belong on the
// public Config (the path/profile selectors are consumed once, during
// LoadDefaultConfig, and persisting them on Config would be misleading
// since Config doesn't re-read the file).
type loadState struct {
	cfg              Config
	sharedConfigPath string
	profileOverride  string
}

// LoadOption mutates the resolution state during LoadDefaultConfig.
//
// Mirrors the AWS SDK v2 functional-option pattern; pass values as
// arguments to LoadDefaultConfig to override a specific field without
// disturbing the rest of the chain.
type LoadOption func(*loadState)

// WithRegion forces the region, bypassing env vars and the config file.
func WithRegion(region string) LoadOption {
	return func(s *loadState) { s.cfg.Region = region }
}

// WithEnvironment forces the environment (public / financial / gov),
// bypassing env vars and the config file.
func WithEnvironment(env Environment) LoadOption {
	return func(s *loadState) { s.cfg.Env = env }
}

// WithCredentialsProvider forces the credentials provider, bypassing
// auth.DefaultChain().
func WithCredentialsProvider(p auth.Provider) LoadOption {
	return func(s *loadState) { s.cfg.Creds = p }
}

// WithProfile selects a non-DEFAULT profile in the shared config file.
//
// Equivalent to setting $NCLOUD_PROFILE; the explicit option wins when
// both are present.
func WithProfile(profile string) LoadOption {
	return func(s *loadState) { s.profileOverride = profile }
}

// WithSharedConfigPath overrides the shared config file path. Empty
// string falls back to the XDG search.
func WithSharedConfigPath(path string) LoadOption {
	return func(s *loadState) { s.sharedConfigPath = path }
}

// LoadDefaultConfig assembles a Config by walking the precedence chain
// (programmatic > env > shared file > defaults).
//
// Returns a populated Config even when the shared file is missing —
// only credential resolution is fatal, and even that is deferred to
// the first NewHTTPClient / NewS3HTTPClient call so a caller wanting
// just region/environment can use this without IAM keys present.
//
//	cfg, err := ncloud.LoadDefaultConfig(ctx)
//	if err != nil { return err }
//	cfg.Region = "kr"   // override after load if you prefer
//
// Typical usage with a per-service helper (once endpoints.gen.go is
// emitted):
//
//	cfg, _ := ncloud.LoadDefaultConfig(ctx)
//	c, _   := wms.NewFromConfig(cfg)
func LoadDefaultConfig(ctx context.Context, opts ...LoadOption) (Config, error) {
	st := loadState{}

	// Apply caller overrides FIRST so we know which slots to skip when
	// reading env vars and the shared file. This matches the AWS SDK
	// v2 semantics — explicit args win, period.
	for _, o := range opts {
		o(&st)
	}

	// 2. Environment variables.
	if st.cfg.Region == "" {
		if v := os.Getenv("NCLOUD_REGION"); v != "" {
			st.cfg.Region = strings.ToLower(v)
		}
	}
	if st.cfg.Env == "" {
		if v := os.Getenv("NCLOUD_ENVIRONMENT"); v != "" {
			st.cfg.Env = Environment(strings.ToLower(v))
		}
	}

	// 3. Shared config file. Only consulted for slots still empty after
	// programmatic + env. Missing file is not an error — the caller
	// might be running entirely on env vars.
	if st.cfg.Region == "" || st.cfg.Env == "" {
		fp := auth.FileProvider{Path: st.sharedConfigPath, Profile: st.profileOverride}
		_, region, env, ferr := fp.FileSettings()
		if ferr == nil {
			if st.cfg.Region == "" && region != "" {
				st.cfg.Region = strings.ToLower(region)
			}
			if st.cfg.Env == "" && env != "" {
				st.cfg.Env = Environment(strings.ToLower(env))
			}
		}
		// Swallow ferr: the file is optional, and the credential chain
		// will surface a usable error later if it really is missing.
	}

	// 4. Hard-coded defaults.
	if st.cfg.Region == "" {
		st.cfg.Region = "kr"
	}
	if st.cfg.Env == "" {
		st.cfg.Env = EnvPublic
	}

	// Credentials: when not pinned by WithCredentialsProvider, use a
	// chain that routes the file step through the SAME path/profile
	// the caller selected, so a non-DEFAULT profile sees consistent
	// credentials and settings.
	if st.cfg.Creds == nil {
		st.cfg.Creds = auth.ChainProvider{Providers: []auth.Provider{
			auth.EnvProvider{},
			auth.FileProvider{Path: st.sharedConfigPath, Profile: st.profileOverride},
		}}
	}

	// Validate the environment string — typo'd values would otherwise
	// silently route to the public apex.
	switch st.cfg.Env {
	case EnvPublic, EnvFinancial, EnvGov:
	default:
		return Config{}, errors.New("ncloud: invalid environment " + string(st.cfg.Env) + " (want public|financial|gov)")
	}

	_ = ctx // reserved for future async credential resolution

	return st.cfg, nil
}

// AsS3Config projects the SigV4-relevant fields from a Config so a
// caller who already loaded a unified Config can hand it to
// NewS3HTTPClient without re-stating Creds/Region/Timeout/Transport.
//
// The Env field is intentionally NOT carried over — Object Storage
// uses fixed regional hosts (kr/sg/jp/de/usw under
// .object.ncloudstorage.com) rather than the public/financial/gov
// apex split.
func (c Config) AsS3Config() S3Config {
	return S3Config{
		Creds:     c.Creds,
		Region:    c.Region,
		Timeout:   c.Timeout,
		Transport: c.Transport,
		Retry:     c.Retry,
	}
}
