package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/trustroots/nostroots/vibe/wrapster/internal/access"
)

const DefaultTargetsConfigPath = "conf.toml"

type Config struct {
	ListenAddr          string
	PublicRelayURL      string
	UpstreamRelayURL    string
	AdditionalRelays    []string
	AuthCachePath       string
	TrustrootsNIP05Base string
	AuthCacheTTL        time.Duration
	AuthEventMaxAge     time.Duration
	UpstreamTimeout     time.Duration
	AdminPubkeys        []string
	AdminAuthMaxAge     time.Duration
	FIPSPeerNpub        string
	FIPSPeerAddr        string
	FIPSNsecPath        string
	MediaConnectorURL   string
	MediaConnectorToken string
	MediaTransportLabel string
	MediaGrantPubkeys   []string
	MediaAuthMaxAge     time.Duration
	MediaHTTPTimeout    time.Duration
	AccessRules         map[string]access.Rule
	Proxy               ProxyConfig
	Media               MediaConfig
	Wiki                map[string]WikiConfig
}

type ProxyConfig struct {
	DefaultTarget   string
	Targets         map[string]string
	AccessRules     []string
	AllowedOrigins  []string
	UpstreamTimeout time.Duration
	MaxBodyBytes    int64
}

type MediaConfig struct {
	Services map[string]MediaServiceConfig
}

type MediaServiceConfig struct {
	AccessRules []string
}

type WikiConfig struct {
	Origin            string
	Label             string
	Summary           string
	WikiPath          string
	WikiAPIPath       string
	WikiLoadPath      string
	WikiMainPagePath  string
	WikiMainPageTitle string
	ProxyRoute        string
	Status            string
	Audience          []string
}

func Load() (Config, error) {
	return LoadWithArgs(os.Args[1:])
}

func LoadWithArgs(args []string) (Config, error) {
	targetsPath, err := targetsConfigPath(args)
	if err != nil {
		return Config{}, err
	}
	fileCfg, err := loadConfigFile(targetsPath)
	if err != nil {
		return Config{}, err
	}

	// The owner_npub is the relay operator and is always treated as an admin.
	adminPubkeys := append([]string{}, fileCfg.AdminPubkeys...)
	if strings.TrimSpace(fileCfg.OwnerPubkey) != "" {
		adminPubkeys = append(adminPubkeys, fileCfg.OwnerPubkey)
	}
	adminPubkeys = append(adminPubkeys, envList("ADMIN_PUBKEYS")...)

	cfg := Config{
		ListenAddr:          env("LISTEN_ADDR", ":5542"),
		PublicRelayURL:      env("PUBLIC_RELAY_URL", "ws://localhost:5542"),
		UpstreamRelayURL:    env("UPSTREAM_RELAY_URL", "ws://strfry:5543"),
		AdditionalRelays:    fileCfg.AdditionalRelays,
		AuthCachePath:       env("AUTH_CACHE_PATH", "./auth-cache.db"),
		TrustrootsNIP05Base: env("TRUSTROOTS_NIP05_BASE_URL", "https://www.trustroots.org/.well-known/nostr.json"),
		AuthCacheTTL:        envDuration("AUTH_CACHE_TTL", 24*time.Hour),
		AuthEventMaxAge:     envDuration("AUTH_EVENT_MAX_AGE", 10*time.Minute),
		UpstreamTimeout:     envDuration("RELAY_UPSTREAM_TIMEOUT", envDuration("UPSTREAM_TIMEOUT", 5*time.Second)),
		AdminPubkeys:        adminPubkeys,
		AdminAuthMaxAge:     envDuration("ADMIN_AUTH_MAX_AGE", 60*time.Second),
		FIPSPeerNpub:        strings.TrimSpace(env("FIPS_HOME_NPUB", env("FIPS_PEER_NPUB", ""))),
		FIPSPeerAddr:        strings.TrimSpace(env("FIPS_HOME_ADDR", env("FIPS_PEER_ADDR", ""))),
		FIPSNsecPath:        env("FIPS_NSEC_PATH", ""),
		MediaConnectorURL:   env("MEDIA_CONNECTOR_BASE_URL", ""),
		MediaConnectorToken: env("MEDIA_CONNECTOR_TOKEN", ""),
		MediaTransportLabel: env("MEDIA_TRANSPORT_LABEL", "private"),
		MediaGrantPubkeys:   envList("MEDIA_GRANT_PUBKEYS"),
		MediaAuthMaxAge:     envDuration("MEDIA_AUTH_MAX_AGE", 60*time.Second),
		MediaHTTPTimeout:    envDuration("MEDIA_HTTP_TIMEOUT", 30*time.Second),
		AccessRules:         fileCfg.AccessRules,
		Proxy: ProxyConfig{
			DefaultTarget:   fileCfg.Targets["trustroots"],
			Targets:         fileCfg.Targets,
			AccessRules:     fileCfg.ProxyAccessRules,
			AllowedOrigins:  envList("ALLOWED_ORIGINS"),
			UpstreamTimeout: envDuration("PROXY_UPSTREAM_TIMEOUT", envDuration("UPSTREAM_TIMEOUT", 15*time.Second)),
			MaxBodyBytes:    envInt64("PROXY_MAX_BODY_BYTES", envInt64("MAX_BODY_BYTES", 10*1024*1024)),
		},
		Media: MediaConfig{
			Services: fileCfg.MediaServices,
		},
		Wiki: fileCfg.Wiki,
	}
	applyAccessRuleDefaults(cfg.AccessRules, cfg.TrustrootsNIP05Base)

	if _, err := url.ParseRequestURI(cfg.PublicRelayURL); err != nil {
		return Config{}, fmt.Errorf("PUBLIC_RELAY_URL is invalid: %w", err)
	}
	if u, err := url.Parse(cfg.UpstreamRelayURL); err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return Config{}, fmt.Errorf("UPSTREAM_RELAY_URL must be ws:// or wss://")
	}
	if cfg.AuthCacheTTL <= 0 {
		return Config{}, fmt.Errorf("AUTH_CACHE_TTL must be positive")
	}
	if cfg.AuthEventMaxAge <= 0 {
		return Config{}, fmt.Errorf("AUTH_EVENT_MAX_AGE must be positive")
	}
	if cfg.UpstreamTimeout <= 0 {
		return Config{}, fmt.Errorf("RELAY_UPSTREAM_TIMEOUT must be positive")
	}
	if cfg.AdminAuthMaxAge <= 0 {
		return Config{}, fmt.Errorf("ADMIN_AUTH_MAX_AGE must be positive")
	}
	if cfg.MediaConnectorURL != "" {
		if u, err := url.Parse(cfg.MediaConnectorURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("MEDIA_CONNECTOR_BASE_URL must be http:// or https://")
		}
	}
	if cfg.MediaAuthMaxAge <= 0 {
		return Config{}, fmt.Errorf("MEDIA_AUTH_MAX_AGE must be positive")
	}
	if cfg.MediaHTTPTimeout <= 0 {
		return Config{}, fmt.Errorf("MEDIA_HTTP_TIMEOUT must be positive")
	}
	if cfg.Proxy.UpstreamTimeout <= 0 {
		return Config{}, fmt.Errorf("PROXY_UPSTREAM_TIMEOUT must be positive")
	}
	if cfg.Proxy.MaxBodyBytes <= 0 {
		return Config{}, fmt.Errorf("PROXY_MAX_BODY_BYTES must be positive")
	}
	for platform, target := range cfg.Proxy.Targets {
		if err := validateRouteKey(platform); err != nil {
			return Config{}, err
		}
		if err := validateTarget("targets."+platform, target); err != nil {
			return Config{}, err
		}
	}
	for _, origin := range cfg.Proxy.AllowedOrigins {
		if err := validateOrigin(origin); err != nil {
			return Config{}, err
		}
	}
	if err := validateAccessRules(cfg.AccessRules, cfg.Proxy.AccessRules, cfg.Media.Services); err != nil {
		return Config{}, err
	}
	if err := validateWikiConfig(cfg.Wiki, cfg.Proxy.Targets); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func validateWikiConfig(wikis map[string]WikiConfig, proxyTargets map[string]string) error {
	for slug, wiki := range wikis {
		if err := validateRouteKey(slug); err != nil {
			return fmt.Errorf("wiki.%s slug is invalid: %w", slug, err)
		}
		if strings.TrimSpace(wiki.Origin) == "" {
			return fmt.Errorf("wiki.%s origin is required", slug)
		}
		if err := validateTarget("wiki."+slug+".origin", wiki.Origin); err != nil {
			return err
		}
		if strings.TrimSpace(wiki.ProxyRoute) == "" {
			return fmt.Errorf("wiki.%s proxy_route is required", slug)
		}
		if _, ok := proxyTargets[wiki.ProxyRoute]; !ok {
			return fmt.Errorf("wiki.%s proxy_route %q is not in configured proxy targets", slug, wiki.ProxyRoute)
		}
		for _, value := range []struct {
			name string
			path string
		}{
			{"wiki_path", wiki.WikiPath},
			{"wiki_api_path", wiki.WikiAPIPath},
			{"wiki_load_path", wiki.WikiLoadPath},
			{"wiki_main_page_path", wiki.WikiMainPagePath},
		} {
			if strings.TrimSpace(value.path) == "" {
				return fmt.Errorf("wiki.%s %s is required", slug, value.name)
			}
			if !strings.HasPrefix(value.path, "/") {
				return fmt.Errorf("wiki.%s %s must start with /", slug, value.name)
			}
		}
	}
	return nil
}

func applyAccessRuleDefaults(rules map[string]access.Rule, trustrootsNIP05Base string) {
	for name, rule := range rules {
		if strings.TrimSpace(rule.RelayURL) == "" {
			rule.RelayURL = access.DefaultRelayURL
		}
		if rule.Type == access.RuleTrustrootsNIP05 && strings.TrimSpace(rule.NIP05BaseURL) == "" {
			rule.NIP05BaseURL = trustrootsNIP05Base
		}
		if rule.Type == access.RuleNostrFollow && strings.TrimSpace(rule.Relationship) == "" {
			rule.Relationship = "owner_follows_user"
		}
		rules[name] = rule
	}
}

func validateAccessRules(rules map[string]access.Rule, proxyRules []string, services map[string]MediaServiceConfig) error {
	for _, proxyRule := range proxyRules {
		if strings.TrimSpace(proxyRule) == "" {
			continue
		}
		if _, ok := rules[proxyRule]; !ok {
			return fmt.Errorf("proxy access rule %q is not defined", proxyRule)
		}
	}
	for service, svc := range services {
		for _, serviceRule := range svc.AccessRules {
			if strings.TrimSpace(serviceRule) == "" {
				continue
			}
			if _, ok := rules[serviceRule]; !ok {
				return fmt.Errorf("media service %q access rule %q is not defined", service, serviceRule)
			}
		}
	}
	for name, rule := range rules {
		switch rule.Type {
		case access.RuleTrustrootsNIP05:
			if strings.TrimSpace(rule.NIP05BaseURL) == "" {
				return fmt.Errorf("access rule %q nip05_base_url is required", name)
			}
		case access.RuleNostrFollow:
			if rule.Relationship != "" && rule.Relationship != "owner_follows_user" {
				return fmt.Errorf("access rule %q relationship must be owner_follows_user", name)
			}
			if strings.TrimSpace(rule.OwnerPubkey) == "" {
				return fmt.Errorf("access rule %q owner_pubkey is required", name)
			}
		default:
			return fmt.Errorf("access rule %q has unsupported type %q", name, rule.Type)
		}
		if strings.TrimSpace(rule.RelayURL) == "" {
			return fmt.Errorf("access rule %q relay is required", name)
		}
	}
	return nil
}

type ConnectorConfig struct {
	ListenAddr        string
	SetupListenAddr   string
	ConfigPath        string
	AllowedCIDRs      []string
	SharedToken       string
	SetupAdminPubkeys []string
	SetupAuthMaxAge   time.Duration
	FIPSNsecPath      string
	FIPSPeerNpub      string
	FIPSPeerAddr      string
	JellyfinBaseURL   string
	JellyfinAPIKey    string
	PlexBaseURL       string
	PlexToken         string
	HTTPTimeout       time.Duration
}

func LoadConnector() (ConnectorConfig, error) {
	cfg := ConnectorConfig{
		ListenAddr:        env("CONNECTOR_LISTEN_ADDR", ":22000"),
		SetupListenAddr:   env("CONNECTOR_SETUP_LISTEN_ADDR", ":22001"),
		ConfigPath:        env("CONNECTOR_CONFIG_PATH", "/data/connector-config.json"),
		AllowedCIDRs:      envListDefault("CONNECTOR_ALLOWED_CIDRS", "10.77.0.1/32,127.0.0.1/32,::1/128"),
		SharedToken:       env("CONNECTOR_SHARED_TOKEN", ""),
		SetupAdminPubkeys: envList("CONNECTOR_ADMIN_PUBKEYS"),
		SetupAuthMaxAge:   envDuration("CONNECTOR_SETUP_AUTH_MAX_AGE", 60*time.Second),
		FIPSNsecPath:      env("FIPS_NSEC_PATH", ""),
		FIPSPeerNpub:      strings.TrimSpace(env("FIPS_PEER_NPUB", env("FIPS_PUBLIC_NPUB", ""))),
		FIPSPeerAddr:      strings.TrimSpace(env("FIPS_PEER_ADDR", env("FIPS_PUBLIC_ADDR", ""))),
		JellyfinBaseURL:   strings.TrimRight(env("JELLYFIN_BASE_URL", ""), "/"),
		JellyfinAPIKey:    env("JELLYFIN_API_KEY", ""),
		PlexBaseURL:       strings.TrimRight(env("PLEX_BASE_URL", ""), "/"),
		PlexToken:         env("PLEX_TOKEN", ""),
		HTTPTimeout:       envDuration("CONNECTOR_HTTP_TIMEOUT", 30*time.Second),
	}
	if cfg.JellyfinBaseURL != "" {
		if u, err := url.Parse(cfg.JellyfinBaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return ConnectorConfig{}, fmt.Errorf("JELLYFIN_BASE_URL must be http:// or https://")
		}
	}
	if cfg.PlexBaseURL != "" {
		if u, err := url.Parse(cfg.PlexBaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return ConnectorConfig{}, fmt.Errorf("PLEX_BASE_URL must be http:// or https://")
		}
	}
	if cfg.HTTPTimeout <= 0 {
		return ConnectorConfig{}, fmt.Errorf("CONNECTOR_HTTP_TIMEOUT must be positive")
	}
	if cfg.SetupAuthMaxAge <= 0 {
		return ConnectorConfig{}, fmt.Errorf("CONNECTOR_SETUP_AUTH_MAX_AGE must be positive")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(value); err == nil {
		return parsed
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envList(key string) []string {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	return splitList(value)
}

func envListDefault(key, fallback string) []string {
	value := os.Getenv(key)
	if value == "" {
		value = fallback
	}
	return splitList(value)
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
