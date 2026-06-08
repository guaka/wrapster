package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTargets(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conf.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaultTargetsConfig(t *testing.T) {
	t.Setenv("TARGETS_CONFIG_PATH", "")

	cfg, err := LoadWithArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"trustroots":          "https://www.trustroots.org",
		"hitchwiki.org":       "https://hitchwiki.org",
		"nomadwiki.org":       "https://nomadwiki.org",
		"wiki.trustroots.org": "https://wiki.trustroots.org",
	}
	for key, value := range want {
		if cfg.Proxy.Targets[key] != value {
			t.Fatalf("target %s = %q, want %q", key, cfg.Proxy.Targets[key], value)
		}
	}
	if cfg.Proxy.DefaultTarget != want["trustroots"] {
		t.Fatalf("default target = %q", cfg.Proxy.DefaultTarget)
	}
}

func TestLoadTargetsConfigFromEnv(t *testing.T) {
	path := writeTargets(t, `targets = [
  "https://www.example.org",
  "https://hitchwiki.org",
]
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	cfg, err := LoadWithArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Targets["hitchwiki.org"] != "https://hitchwiki.org" {
		t.Fatalf("env target = %q", cfg.Proxy.Targets["hitchwiki.org"])
	}
}

func TestLoadTargetsConfigFromTable(t *testing.T) {
	path := writeTargets(t, `[targets]
trustroots = "https://example.org"
"hitchwiki.org" = "https://hitchwiki.example"
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	cfg, err := LoadWithArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Targets["hitchwiki.org"] != "https://hitchwiki.example" {
		t.Fatalf("table target = %q", cfg.Proxy.Targets["hitchwiki.org"])
	}
}

func TestLoadTargetsConfigFromFlag(t *testing.T) {
	path := writeTargets(t, `[targets]
trustroots = "https://flag.example"
`)
	t.Setenv("TARGETS_CONFIG_PATH", "")

	cfg, err := LoadWithArgs([]string{"--targets-config", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.DefaultTarget != "https://flag.example" {
		t.Fatalf("default target = %q", cfg.Proxy.DefaultTarget)
	}
}

func TestTargetsConfigFlagWinsOverEnv(t *testing.T) {
	envPath := writeTargets(t, `[targets]
trustroots = "https://env.example"
`)
	flagPath := writeTargets(t, `[targets]
trustroots = "https://flag.example"
`)
	t.Setenv("TARGETS_CONFIG_PATH", envPath)

	cfg, err := LoadWithArgs([]string{"--targets-config", flagPath})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.DefaultTarget != "https://flag.example" {
		t.Fatalf("default target = %q", cfg.Proxy.DefaultTarget)
	}
}

func TestInvalidTargetsTOML(t *testing.T) {
	path := writeTargets(t, `[targets]
trustroots "https://example.org"
`)
	t.Setenv("TARGETS_CONFIG_PATH", "")

	if _, err := LoadWithArgs([]string{"--targets-config", path}); err == nil {
		t.Fatal("expected invalid TOML error")
	}
}

func TestInvalidTargetURL(t *testing.T) {
	path := writeTargets(t, `[targets]
trustroots = "ftp://example.org"
`)
	t.Setenv("TARGETS_CONFIG_PATH", "")

	if _, err := LoadWithArgs([]string{"--targets-config", path}); err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestTargetsListDerivesRouteKeys(t *testing.T) {
	path := writeTargets(t, `targets = [
  "https://www.trustroots.org",
  "https://hitchwiki.org",
  "https://nomadwiki.org",
  "https://wiki.trustroots.org",
]
`)
	t.Setenv("TARGETS_CONFIG_PATH", "")

	cfg, err := LoadWithArgs([]string{"--targets-config", path})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"trustroots":          "https://www.trustroots.org",
		"hitchwiki.org":       "https://hitchwiki.org",
		"nomadwiki.org":       "https://nomadwiki.org",
		"wiki.trustroots.org": "https://wiki.trustroots.org",
	}
	for key, value := range want {
		if cfg.Proxy.Targets[key] != value {
			t.Fatalf("target %s = %q, want %q", key, cfg.Proxy.Targets[key], value)
		}
	}
	if cfg.Proxy.DefaultTarget != want["trustroots"] {
		t.Fatalf("default target = %q", cfg.Proxy.DefaultTarget)
	}
}

func TestTargetsListAcceptsSingleLineArray(t *testing.T) {
	path := writeTargets(t, `targets = ["https://www.trustroots.org", "https://hitchwiki.org"]`)
	t.Setenv("TARGETS_CONFIG_PATH", "")

	cfg, err := LoadWithArgs([]string{"--targets-config", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Targets["trustroots"] != "https://www.trustroots.org" {
		t.Fatalf("trustroots target = %q", cfg.Proxy.Targets["trustroots"])
	}
	if cfg.Proxy.Targets["hitchwiki.org"] != "https://hitchwiki.org" {
		t.Fatalf("hitchwiki target = %q", cfg.Proxy.Targets["hitchwiki.org"])
	}
}

func TestProxyEnvNames(t *testing.T) {
	path := writeTargets(t, `[targets]
trustroots = "https://example.org"
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)
	t.Setenv("PROXY_UPSTREAM_TIMEOUT", "3s")
	t.Setenv("PROXY_MAX_BODY_BYTES", "2048")

	cfg, err := LoadWithArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.UpstreamTimeout.String() != "3s" {
		t.Fatalf("proxy timeout = %s", cfg.Proxy.UpstreamTimeout)
	}
	if cfg.Proxy.MaxBodyBytes != 2048 {
		t.Fatalf("proxy max body = %d", cfg.Proxy.MaxBodyBytes)
	}
}
