package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trustroots/nostroots/vibe/wrapster/internal/access"
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
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "conf.toml"), []byte(`targets = [
  "https://www.trustroots.org",
  "https://hitchwiki.org",
  "https://nomadwiki.org",
  "https://wiki.trustroots.org",
]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

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

func TestMissingDefaultConfigMentionsConfToml(t *testing.T) {
	t.Setenv("TARGETS_CONFIG_PATH", "")
	t.Chdir(t.TempDir())

	_, err := LoadWithArgs(nil)
	if err == nil {
		t.Fatal("expected missing conf.toml error")
	}
	if !strings.Contains(err.Error(), "conf.toml not found") {
		t.Fatalf("error = %q, want conf.toml not found", err.Error())
	}
	if strings.Contains(err.Error(), "conf.toml.example") {
		t.Fatalf("error mentioned conf.toml.example: %q", err.Error())
	}
}

func TestLoadFriendlyProxyGroupConfig(t *testing.T) {
	path := writeTargets(t, `owner_npub = "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6"
additional_relays = ["wss://nip42.trustroots.org"]
access_rule = {"nip05_domain": "trustroots.org"}

[proxy_group.hospex]
urls = ["https://www.trustroots.org",
  "https://hitchwiki.org",
  "https://nomadwiki.org",
  "https://wiki.trustroots.org",
]

[proxy_group.media]
urls = ["fips_jellyfin", "fips_plex"]
additional_access_rule = ["nostr_follow"]
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	cfg, err := LoadWithArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	wantTargets := map[string]string{
		"trustroots":          "https://www.trustroots.org",
		"hitchwiki.org":       "https://hitchwiki.org",
		"nomadwiki.org":       "https://nomadwiki.org",
		"wiki.trustroots.org": "https://wiki.trustroots.org",
	}
	for key, want := range wantTargets {
		if got := cfg.Proxy.Targets[key]; got != want {
			t.Fatalf("target %s = %q, want %q", key, got, want)
		}
	}
	if !slices.Equal(cfg.Proxy.AccessRules, []string{"trustroots_nip05"}) {
		t.Fatalf("proxy access rules = %#v", cfg.Proxy.AccessRules)
	}
	proxyRule := cfg.AccessRules["trustroots_nip05"]
	if proxyRule.Type != access.RuleTrustrootsNIP05 || proxyRule.RelayURL != "wss://nip42.trustroots.org" || proxyRule.NIP05BaseURL != "https://www.trustroots.org/.well-known/nostr.json" {
		t.Fatalf("proxy rule = %#v", proxyRule)
	}
	wantMediaRules := []string{"trustroots_nip05", "media_owner_follows"}
	if !slices.Equal(cfg.Media.Services["jellyfin"].AccessRules, wantMediaRules) || !slices.Equal(cfg.Media.Services["plex"].AccessRules, wantMediaRules) {
		t.Fatalf("media services = %#v", cfg.Media.Services)
	}
	ownerPubkey, err := access.NormalizePubkey("npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6")
	if err != nil {
		t.Fatal(err)
	}
	mediaRule := cfg.AccessRules["media_owner_follows"]
	if mediaRule.Type != access.RuleNostrFollow || mediaRule.OwnerPubkey != ownerPubkey || mediaRule.RelayURL != "wss://nip42.trustroots.org" {
		t.Fatalf("media rule = %#v", mediaRule)
	}
}

func TestOwnerNpubIsAdmin(t *testing.T) {
	path := writeTargets(t, `owner_npub = "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6"
additional_relays = ["wss://nip42.trustroots.org"]
access_rule = {"nip05_domain": "trustroots.org"}

[proxy_group.hospex]
urls = ["https://www.trustroots.org"]
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	cfg, err := LoadWithArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerPubkey, err := access.NormalizePubkey("npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pubkey := range cfg.AdminPubkeys {
		if pubkey == ownerPubkey {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("owner pubkey %q not in admin pubkeys %v", ownerPubkey, cfg.AdminPubkeys)
	}
}

func TestFriendlyConfigAcceptsFIPSMediaAliases(t *testing.T) {
	path := writeTargets(t, `owner_npub = "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6"
additional_relays = ["wss://nip42.trustroots.org"]
access_rule = {"nip05_domain": "trustroots.org"}

[proxy_group.hospex]
urls = ["https://www.trustroots.org"]

[proxy_group.media]
urls = ["fips_jellyfin", "fips_plex"]
additional_access_rule = ["nostr_follow"]
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	cfg, err := LoadWithArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	wantMediaRules := []string{"trustroots_nip05", "media_owner_follows"}
	if !slices.Equal(cfg.Media.Services["jellyfin"].AccessRules, wantMediaRules) || !slices.Equal(cfg.Media.Services["plex"].AccessRules, wantMediaRules) {
		t.Fatalf("media services = %#v", cfg.Media.Services)
	}
}

func TestFriendlyConfigRequiresValidOwnerNpub(t *testing.T) {
	path := writeTargets(t, `owner_npub = "npub1999example"
additional_relays = ["wss://nip42.trustroots.org"]
access_rule = {"nip05_domain": "trustroots.org"}

[proxy_group.hospex]
urls = ["https://www.trustroots.org"]

[proxy_group.media]
urls = ["fips_jellyfin"]
additional_access_rule = ["nostr_follow"]
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	_, err := LoadWithArgs(nil)
	if err == nil {
		t.Fatal("expected invalid owner_npub error")
	}
	if !strings.Contains(err.Error(), "owner_npub is invalid") {
		t.Fatalf("error = %q, want owner_npub is invalid", err.Error())
	}
}

func TestFriendlyConfigRequiresOwnerNpubForNostrFollow(t *testing.T) {
	path := writeTargets(t, `access_rule = {"nip05_domain": "trustroots.org"}

[proxy_group.media]
urls = ["fips_jellyfin"]
additional_access_rule = ["nostr_follow"]
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	_, err := LoadWithArgs(nil)
	if err == nil {
		t.Fatal("expected missing owner_npub error")
	}
	if !strings.Contains(err.Error(), "owner_npub is required") {
		t.Fatalf("error = %q, want owner_npub is required", err.Error())
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

func TestSimpleTargetsUseGlobalAccessRule(t *testing.T) {
	path := writeTargets(t, `targets = ["https://www.trustroots.org"]
access_rule = {"nip05_domain": "trustroots.org"}
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	cfg, err := LoadWithArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Proxy.AccessRules, []string{"trustroots_nip05"}) {
		t.Fatalf("proxy access rules = %#v", cfg.Proxy.AccessRules)
	}
}

func TestUnsupportedGlobalAccessCriterionFails(t *testing.T) {
	path := writeTargets(t, `targets = ["https://www.trustroots.org"]
access_rule = {"role": "member"}
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	_, err := LoadWithArgs(nil)
	if err == nil {
		t.Fatal("expected unsupported global access criterion error")
	}
	if !strings.Contains(err.Error(), `access_rule contains unsupported criterion "role"`) {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestUnsupportedAdditionalAccessRuleFails(t *testing.T) {
	path := writeTargets(t, `owner_npub = "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6"
access_rule = {"nip05_domain": "trustroots.org"}

[proxy_group.media]
urls = ["fips_jellyfin"]
additional_access_rule = ["invite_code"]
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	_, err := LoadWithArgs(nil)
	if err == nil {
		t.Fatal("expected unsupported additional access rule error")
	}
	if !strings.Contains(err.Error(), `additional_access_rule contains unsupported rule "invite_code"`) {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestOldPerGroupAccessFails(t *testing.T) {
	path := writeTargets(t, `[proxy_group.hospex]
urls = ["https://www.trustroots.org"]
access = {"nip05_domain": "trustroots.org"}
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	_, err := LoadWithArgs(nil)
	if err == nil {
		t.Fatal("expected old per-group access error")
	}
	if !strings.Contains(err.Error(), "unsupported table proxy_group.hospex.access") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestOldExplicitAccessControlConfigFails(t *testing.T) {
	path := writeTargets(t, `targets = ["https://www.trustroots.org"]

[access_rules.trustroots_nip05]
type = "trustroots_nip05"
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	_, err := LoadWithArgs(nil)
	if err == nil {
		t.Fatal("expected old explicit access rule error")
	}
	if !strings.Contains(err.Error(), "unsupported config key access_rules.trustroots_nip05.type") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestOldProxyAccessRuleConfigFails(t *testing.T) {
	path := writeTargets(t, `targets = ["https://www.trustroots.org"]

[proxy]
access_rule = "trustroots_nip05"
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	_, err := LoadWithArgs(nil)
	if err == nil {
		t.Fatal("expected old proxy access rule error")
	}
	if !strings.Contains(err.Error(), "unsupported config key proxy.access_rule") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestOldMediaServiceAccessRuleConfigFails(t *testing.T) {
	path := writeTargets(t, `targets = ["https://www.trustroots.org"]

[media.services.jellyfin]
access_rule = "media_owner_follows"
`)
	t.Setenv("TARGETS_CONFIG_PATH", path)

	_, err := LoadWithArgs(nil)
	if err == nil {
		t.Fatal("expected old media service access rule error")
	}
	if !strings.Contains(err.Error(), "unsupported config key media.services.jellyfin.access_rule") {
		t.Fatalf("error = %q", err.Error())
	}
}
