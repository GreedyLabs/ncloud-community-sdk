package ncloud

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/greedylabs/ncloud-community-sdk/sdk/auth"
)

// snapshotEnv returns a function that restores the original env-var
// values, so individual tests can mutate the process env without
// leaking into siblings. We can't use t.Setenv directly because we
// also need to UNSET vars and t.Setenv in older Go versions doesn't
// reset between subtests cleanly.
func snapshotEnv(t *testing.T, keys ...string) {
	t.Helper()
	saved := make(map[string]string, len(keys))
	present := make(map[string]bool, len(keys))
	for _, k := range keys {
		v, ok := os.LookupEnv(k)
		saved[k] = v
		present[k] = ok
		_ = os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range keys {
			if present[k] {
				_ = os.Setenv(k, saved[k])
			} else {
				_ = os.Unsetenv(k)
			}
		}
	})
}

func TestLoadDefaultConfig_HardcodedDefaults(t *testing.T) {
	snapshotEnv(t, "NCLOUD_REGION", "NCLOUD_ENVIRONMENT", "XDG_CONFIG_HOME", "HOME", "NCLOUD_PROFILE")
	// Point HOME at an empty dir so file step finds nothing.
	tmp := t.TempDir()
	_ = os.Setenv("HOME", tmp)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))

	cfg, err := LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	if cfg.Region != "kr" {
		t.Errorf("Region = %q, want kr", cfg.Region)
	}
	if cfg.Env != EnvPublic {
		t.Errorf("Env = %q, want public", cfg.Env)
	}
}

func TestLoadDefaultConfig_EnvVarOverridesDefaults(t *testing.T) {
	snapshotEnv(t, "NCLOUD_REGION", "NCLOUD_ENVIRONMENT", "XDG_CONFIG_HOME", "HOME", "NCLOUD_PROFILE")
	tmp := t.TempDir()
	_ = os.Setenv("HOME", tmp)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	_ = os.Setenv("NCLOUD_REGION", "JP")
	_ = os.Setenv("NCLOUD_ENVIRONMENT", "Financial")

	cfg, err := LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	if cfg.Region != "jp" {
		t.Errorf("Region = %q, want jp (lowercased)", cfg.Region)
	}
	if cfg.Env != EnvFinancial {
		t.Errorf("Env = %q, want financial", cfg.Env)
	}
}

func TestLoadDefaultConfig_FileBetweenEnvAndDefaults(t *testing.T) {
	snapshotEnv(t, "NCLOUD_REGION", "NCLOUD_ENVIRONMENT", "XDG_CONFIG_HOME", "HOME", "NCLOUD_PROFILE")
	tmp := t.TempDir()
	_ = os.Setenv("HOME", tmp)

	// Create the XDG config file.
	xdg := filepath.Join(tmp, "xdg")
	_ = os.Setenv("XDG_CONFIG_HOME", xdg)
	cfgDir := filepath.Join(xdg, "ncloud-community")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `[DEFAULT]
ncloud_access_key_id     = test-key
ncloud_secret_access_key = test-secret
ncloud_region            = sg
ncloud_environment       = gov
`
	if err := os.WriteFile(filepath.Join(cfgDir, "configure"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	if cfg.Region != "sg" {
		t.Errorf("Region = %q, want sg", cfg.Region)
	}
	if cfg.Env != EnvGov {
		t.Errorf("Env = %q, want gov", cfg.Env)
	}

	// And the credential chain wired up by LoadDefaultConfig should be
	// able to resolve these too.
	creds, cerr := cfg.Creds.Credentials()
	if cerr != nil {
		t.Fatalf("Creds.Credentials(): %v", cerr)
	}
	if creds.AccessKey != "test-key" || creds.SecretKey != "test-secret" {
		t.Errorf("creds = %+v, want test-key/test-secret", creds)
	}
}

func TestLoadDefaultConfig_ProgrammaticBeatsEnv(t *testing.T) {
	snapshotEnv(t, "NCLOUD_REGION", "NCLOUD_ENVIRONMENT", "XDG_CONFIG_HOME", "HOME", "NCLOUD_PROFILE")
	tmp := t.TempDir()
	_ = os.Setenv("HOME", tmp)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	_ = os.Setenv("NCLOUD_REGION", "kr")
	_ = os.Setenv("NCLOUD_ENVIRONMENT", "public")

	cfg, err := LoadDefaultConfig(context.Background(),
		WithRegion("usw"),
		WithEnvironment(EnvFinancial),
	)
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	if cfg.Region != "usw" || cfg.Env != EnvFinancial {
		t.Errorf("got %q/%q, want usw/financial", cfg.Region, cfg.Env)
	}
}

func TestLoadDefaultConfig_WithCredentialsProviderOverridesChain(t *testing.T) {
	snapshotEnv(t, "NCLOUD_REGION", "NCLOUD_ENVIRONMENT", "XDG_CONFIG_HOME", "HOME", "NCLOUD_PROFILE",
		"NCLOUD_ACCESS_KEY_ID", "NCLOUD_SECRET_KEY")
	tmp := t.TempDir()
	_ = os.Setenv("HOME", tmp)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))

	want := auth.Credentials{AccessKey: "static-key", SecretKey: "static-secret"}
	cfg, err := LoadDefaultConfig(context.Background(),
		WithCredentialsProvider(auth.Static(want)),
	)
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	got, gerr := cfg.Creds.Credentials()
	if gerr != nil {
		t.Fatalf("Credentials(): %v", gerr)
	}
	if got.AccessKey != want.AccessKey || got.SecretKey != want.SecretKey {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestLoadDefaultConfig_RejectsInvalidEnvironment(t *testing.T) {
	snapshotEnv(t, "NCLOUD_REGION", "NCLOUD_ENVIRONMENT", "XDG_CONFIG_HOME", "HOME", "NCLOUD_PROFILE")
	tmp := t.TempDir()
	_ = os.Setenv("HOME", tmp)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	_ = os.Setenv("NCLOUD_ENVIRONMENT", "edge") // not a real env

	_, err := LoadDefaultConfig(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid environment, got nil")
	}
}

func TestLoadDefaultConfig_WithProfileSelectsSection(t *testing.T) {
	snapshotEnv(t, "NCLOUD_REGION", "NCLOUD_ENVIRONMENT", "XDG_CONFIG_HOME", "HOME", "NCLOUD_PROFILE")
	tmp := t.TempDir()
	_ = os.Setenv("HOME", tmp)
	xdg := filepath.Join(tmp, "xdg")
	_ = os.Setenv("XDG_CONFIG_HOME", xdg)
	cfgDir := filepath.Join(xdg, "ncloud-community")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `[DEFAULT]
ncloud_access_key_id     = default-key
ncloud_secret_access_key = default-secret
ncloud_region            = kr

[gov-account]
ncloud_access_key_id     = gov-key
ncloud_secret_access_key = gov-secret
ncloud_region            = jp
ncloud_environment       = gov
`
	if err := os.WriteFile(filepath.Join(cfgDir, "configure"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadDefaultConfig(context.Background(), WithProfile("gov-account"))
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	if cfg.Region != "jp" || cfg.Env != EnvGov {
		t.Errorf("got %q/%q, want jp/gov", cfg.Region, cfg.Env)
	}
	creds, cerr := cfg.Creds.Credentials()
	if cerr != nil {
		t.Fatalf("Credentials(): %v", cerr)
	}
	if creds.AccessKey != "gov-key" {
		t.Errorf("AccessKey = %q, want gov-key", creds.AccessKey)
	}
}

func TestConfig_AsS3Config_PreservesRelevantFields(t *testing.T) {
	cfg := Config{
		Env:    EnvGov, // intentionally not carried
		Region: "sg",
		Creds:  auth.Static(auth.Credentials{AccessKey: "k", SecretKey: "s"}),
	}
	s3 := cfg.AsS3Config()
	if s3.Region != "sg" {
		t.Errorf("Region = %q, want sg", s3.Region)
	}
	if s3.Creds == nil {
		t.Fatal("Creds not propagated")
	}
}
