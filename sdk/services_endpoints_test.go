package ncloud_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ncloud "github.com/greedylabs/ncloud-community-sdk/sdk"
	cloud_log_analytics "github.com/greedylabs/ncloud-community-sdk/sdk/services/cloud_log_analytics"
	nks "github.com/greedylabs/ncloud-community-sdk/sdk/services/nks"
	object_storage "github.com/greedylabs/ncloud-community-sdk/sdk/services/object_storage"
	ses2 "github.com/greedylabs/ncloud-community-sdk/sdk/services/ses2"
	wms "github.com/greedylabs/ncloud-community-sdk/sdk/services/wms"
)

// scratchHome isolates the test process from the developer's real
// XDG/HOME so the file step of ncloud.LoadDefaultConfig finds nothing.
func scratchHome(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	t.Setenv("NCLOUD_REGION", "")
	t.Setenv("NCLOUD_ENVIRONMENT", "")
	t.Setenv("NCLOUD_ACCESS_KEY_ID", "smoke-key")
	t.Setenv("NCLOUD_SECRET_KEY", "smoke-secret")
	_ = os.Unsetenv("NCLOUD_PROFILE")
}

// TestNewFromConfig_FlatService — wms has no region/basePath
// substitution, so the env URL should round-trip verbatim.
func TestNewFromConfig_FlatService(t *testing.T) {
	scratchHome(t)
	cfg, err := ncloud.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c, err := wms.NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("wms.NewFromConfig: %v", err)
	}
	if c == nil {
		t.Fatal("nil client")
	}
	want := "https://wms.apigw.ntruss.com/api/v1/"
	if c.ClientInterface.(*wms.Client).Server != want {
		t.Errorf("Server = %q, want %q", c.ClientInterface.(*wms.Client).Server, want)
	}
}

// TestNewFromConfig_RegionRouted — nks's basePath rules pick
// `nks/v2` for KR but `nks/jp-v2` for JP.
func TestNewFromConfig_RegionRouted(t *testing.T) {
	scratchHome(t)
	cases := []struct {
		region    string
		wantBase  string
	}{
		{"KR", "nks/v2"},
		{"FKR", "nks/v2"},
		{"CSKR", "nks/v2"},
		{"JP", "nks/jp-v2"},
		{"SG", "nks/sg-v2"},
	}
	for _, tc := range cases {
		t.Run(tc.region, func(t *testing.T) {
			cfg, err := ncloud.LoadDefaultConfig(context.Background(), ncloud.WithRegion(tc.region))
			if err != nil {
				t.Fatal(err)
			}
			c, err := nks.NewFromConfig(cfg)
			if err != nil {
				t.Fatalf("nks.NewFromConfig: %v", err)
			}
			got := c.ClientInterface.(*nks.Client).Server
			wantPrefix := "https://nks.apigw.ntruss.com/" + tc.wantBase
			if !strings.HasPrefix(got, wantPrefix) {
				t.Errorf("Server = %q, want prefix %q", got, wantPrefix)
			}
		})
	}
}

// TestNewFromConfig_RegionRouted_FinSubdomain — ses2's financial
// env has the special `fin-vpcsearchengine` subdomain.
func TestNewFromConfig_RegionRouted_FinSubdomain(t *testing.T) {
	scratchHome(t)
	cfg, err := ncloud.LoadDefaultConfig(context.Background(),
		ncloud.WithEnvironment(ncloud.EnvFinancial),
		ncloud.WithRegion("FKR"),
	)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ses2.NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("ses2.NewFromConfig: %v", err)
	}
	got := c.ClientInterface.(*ses2.Client).Server
	want := "https://fin-vpcsearchengine.apigw.fin-ntruss.com/api/v2/"
	if got != want {
		t.Errorf("Server = %q, want %q", got, want)
	}
}

// TestNewFromConfig_S3RegionInHost — object-storage's public env
// substitutes regionCode into the host.
func TestNewFromConfig_S3RegionInHost(t *testing.T) {
	scratchHome(t)
	cfg, err := ncloud.LoadDefaultConfig(context.Background(), ncloud.WithRegion("kr"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := object_storage.NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("object_storage.NewFromConfig: %v", err)
	}
	got := c.ClientInterface.(*object_storage.Client).Server
	want := "https://kr.object.ncloudstorage.com/"
	if got != want {
		t.Errorf("Server = %q, want %q", got, want)
	}
}

// TestNewFromConfig_PathParameterRegion — cloud-log-analytics holds
// regionCode in operation paths, not the server URL, so the resolved
// server should be identical regardless of cfg.Region.
func TestNewFromConfig_PathParameterRegion(t *testing.T) {
	scratchHome(t)
	cfg, _ := ncloud.LoadDefaultConfig(context.Background(), ncloud.WithRegion("JP"))
	c, err := cloud_log_analytics.NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("cloud_log_analytics.NewFromConfig: %v", err)
	}
	got := c.ClientInterface.(*cloud_log_analytics.Client).Server
	want := "https://cloudloganalytics.apigw.ntruss.com/"
	if got != want {
		t.Errorf("Server = %q, want %q", got, want)
	}
}

// TestNewFromConfig_RejectsUnknownEnv — guard against silent fall
// through when an env was not declared in availability.
func TestNewFromConfig_RejectsUnknownEnv(t *testing.T) {
	scratchHome(t)
	cfg, _ := ncloud.LoadDefaultConfig(context.Background())
	cfg.Env = "edge" // bypass ncloud.LoadDefaultConfig's validation
	_, err := wms.NewFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for unknown env, got nil")
	}
	if !strings.Contains(err.Error(), "edge") {
		t.Errorf("error = %q, want it to mention 'edge'", err)
	}
}
