package auth

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Provider loads credentials on demand. Implementations may be static (env
// vars, config file) or dynamic (ncloud metadata API, STS assume-role).
type Provider interface {
	Credentials() (Credentials, error)
}

// Static returns a Provider backed by fixed keys.
func Static(creds Credentials) Provider { return staticProvider{creds} }

type staticProvider struct{ c Credentials }

func (s staticProvider) Credentials() (Credentials, error) {
	if err := s.c.Valid(); err != nil {
		return Credentials{}, err
	}
	return s.c, nil
}

// EnvProvider reads credentials from environment variables:
//
//	NCLOUD_ACCESS_KEY_ID
//	NCLOUD_SECRET_KEY
type EnvProvider struct{}

func (EnvProvider) Credentials() (Credentials, error) {
	c := Credentials{
		AccessKey: os.Getenv("NCLOUD_ACCESS_KEY_ID"),
		SecretKey: os.Getenv("NCLOUD_SECRET_KEY"),
	}
	if err := c.Valid(); err != nil {
		return Credentials{}, errors.New("env: NCLOUD_ACCESS_KEY_ID/NCLOUD_SECRET_KEY not set")
	}
	return c, nil
}

// FileProvider reads NCP credentials from an ini-style file. The default
// search path is XDG-compliant:
//
//	1. $XDG_CONFIG_HOME/ncloud-community/configure  (XDG default)
//	2. $HOME/.config/ncloud-community/configure     (XDG fallback)
//
// We deliberately do NOT read $HOME/.ncloud/configure: that path is
// owned by the official NaverCloudPlatform/ncloud-sdk-go-v2 SDK and
// sharing it would either let our extra keys (ncloud_region,
// ncloud_environment) leak into the official parser or make us
// silently consume a file the user authored for the official SDK.
// Keeping a separate directory lets both SDKs coexist on one machine
// without surprise. Point Path at any path you like (including
// ~/.ncloud/configure) if you really want to share.
//
// File format mirrors the official NCP SDK convention but adds two
// optional keys for region and environment:
//
//	[DEFAULT]
//	ncloud_access_key_id     = ncp_iam_BPASKR...
//	ncloud_secret_access_key = ncp_iam_BPKSKR...
//	ncloud_region            = kr        # optional — kr | sg | jp | de | usw
//	ncloud_environment       = public    # optional — public | financial | gov
//
//	[gov-account]
//	ncloud_access_key_id     = ...
//	ncloud_secret_access_key = ...
//	ncloud_environment       = gov
//
// A custom path/profile can be supplied; pass "" for the defaults.
//
// FileProvider only returns Credentials. The region/environment values
// are read by the higher-level config loader in the root package
// (see ncloud.LoadDefaultConfig).
type FileProvider struct {
	Path    string // default: XDG path
	Profile string // default DEFAULT
}

func (f FileProvider) Credentials() (Credentials, error) {
	path, err := f.resolvePath()
	if err != nil {
		return Credentials{}, err
	}
	profile := f.profile()
	fh, err := os.Open(path)
	if err != nil {
		return Credentials{}, err
	}
	defer fh.Close()
	c, _, _, err := parseIni(fh, profile)
	if err != nil {
		return Credentials{}, err
	}
	if err := c.Valid(); err != nil {
		return Credentials{}, errors.New("file: credentials not found in profile [" + profile + "]")
	}
	return c, nil
}

// FileSettings returns the auxiliary region/environment values from the
// same ini section, alongside the credentials. Empty strings mean the
// key was absent or the section was not found.
//
// This is the entry point the root-package config loader uses; it lets
// us read credentials and settings in one parse pass without forcing a
// caller to hard-code the file format.
func (f FileProvider) FileSettings() (creds Credentials, region, environment string, err error) {
	path, perr := f.resolvePath()
	if perr != nil {
		return Credentials{}, "", "", perr
	}
	fh, oerr := os.Open(path)
	if oerr != nil {
		return Credentials{}, "", "", oerr
	}
	defer fh.Close()
	return parseIni(fh, f.profile())
}

// resolvePath picks the first existing file from the XDG search list.
// If Path is set explicitly it is honoured verbatim.
//
// Note that $HOME/.ncloud/configure (the official NCP SDK's location)
// is intentionally NOT searched — see FileProvider's doc comment. To
// share a file with the official SDK, set Path explicitly.
func (f FileProvider) resolvePath() (string, error) {
	if f.Path != "" {
		return f.Path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	xdgHome := os.Getenv("XDG_CONFIG_HOME")
	if xdgHome == "" {
		xdgHome = filepath.Join(home, ".config")
	}
	candidates := []string{
		filepath.Join(xdgHome, "ncloud-community", "configure"),
		filepath.Join(home, ".config", "ncloud-community", "configure"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// Return the canonical path even when missing, so the caller's error
	// message points at the expected location.
	return candidates[0], nil
}

func (f FileProvider) profile() string {
	if f.Profile != "" {
		return f.Profile
	}
	if env := os.Getenv("NCLOUD_PROFILE"); env != "" {
		return env
	}
	return "DEFAULT"
}

// parseIni reads creds + region + environment from the named profile.
// Returns empty strings for keys not present. Returns no error when the
// section itself is missing — the caller decides whether that is fatal
// (Credentials.Valid() will surface it for the credentials path).
func parseIni(r io.Reader, profile string) (creds Credentials, region, environment string, err error) {
	data, rerr := io.ReadAll(r)
	if rerr != nil {
		return Credentials{}, "", "", rerr
	}
	lines := strings.Split(string(data), "\n")
	inSection := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec := strings.Trim(line, "[]")
			inSection = (sec == profile)
			continue
		}
		if !inSection {
			continue
		}
		if eq := strings.Index(line, "="); eq > 0 {
			k := strings.TrimSpace(line[:eq])
			v := strings.TrimSpace(line[eq+1:])
			switch k {
			case "ncloud_access_key_id":
				creds.AccessKey = v
			case "ncloud_secret_access_key":
				creds.SecretKey = v
			case "ncloud_region":
				region = v
			case "ncloud_environment":
				environment = v
			}
		}
	}
	return creds, region, environment, nil
}

// ErrNoCredentials is the sentinel returned by ChainProvider when no
// inner provider could supply credentials. Callers should use
// errors.Is to test for it rather than string-matching the message:
//
//	creds, err := chain.Credentials()
//	if errors.Is(err, auth.ErrNoCredentials) {
//	    // tell the user to set NCLOUD_ACCESS_KEY_ID/NCLOUD_SECRET_KEY
//	    // or run `ncc configure` etc.
//	}
//
// The full per-provider failure list is wrapped via errors.Join, so
// errors.As / errors.Is can still reach the inner errors when needed
// for diagnostics.
var ErrNoCredentials = errors.New("no credentials available in any provider")

// ChainProvider tries each inner Provider in order and returns the first that
// successfully returns non-empty credentials. When all providers fail, the
// returned error wraps both ErrNoCredentials (so consumers can branch with
// errors.Is) AND the joined per-provider failures (so the message lists
// what was attempted and why each one rejected the request).
type ChainProvider struct {
	Providers []Provider
}

func (c ChainProvider) Credentials() (Credentials, error) {
	var attempts []error
	for _, p := range c.Providers {
		creds, err := p.Credentials()
		if err == nil {
			return creds, nil
		}
		attempts = append(attempts, err)
	}
	if len(attempts) == 0 {
		// No providers configured at all — distinct failure mode from
		// "every provider rejected the request".
		return Credentials{}, fmt.Errorf("%w: chain has no providers configured", ErrNoCredentials)
	}
	return Credentials{}, fmt.Errorf("%w:\n%w", ErrNoCredentials, errors.Join(attempts...))
}

// DefaultChain follows the same precedence pattern as the official
// NCP SDK but uses our own config path so the two SDKs don't fight
// over the same file:
//
//	1. Environment variables (NCLOUD_ACCESS_KEY_ID + NCLOUD_SECRET_KEY)
//	2. $XDG_CONFIG_HOME/ncloud-community/configure  (DEFAULT profile)
//	3. (reserved) Server Role via ncloud metadata API
//
// Server Role support requires a running VM with the metadata endpoint
// reachable; we stub it here and add a full implementation when the
// metadata service is wired up.
func DefaultChain() Provider {
	return ChainProvider{Providers: []Provider{
		EnvProvider{},
		FileProvider{},
		// TODO: ServerRoleProvider{HTTPClient: &http.Client{Timeout: 3 * time.Second}}
	}}
}

// Unused import anchors for the TODO — keep net/http and time visible so the
// planned ServerRoleProvider drops in cleanly later.
var (
	_ = http.DefaultTransport
	_ = time.Second
)
