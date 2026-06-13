package main

import (
	"log"
	"net/http"

	"github.com/trustroots/nostroots/vibe/wrapster/internal/access"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/config"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/media"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/nip05"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/proxy"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/relay"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	cache, err := store.Open(cfg.AuthCachePath)
	if err != nil {
		log.Fatal(err)
	}
	defer cache.Close()

	accessAuth := access.Authorizer{
		Rules:  cfg.AccessRules,
		MaxAge: cfg.MediaAuthMaxAge,
	}
	genericProxy := proxy.New(proxy.Config{
		Prefix:          "/proxy",
		DefaultTarget:   cfg.Proxy.DefaultTarget,
		Targets:         cfg.Proxy.Targets,
		AccessRules:     cfg.Proxy.AccessRules,
		Access:          accessAuth,
		AllowedOrigins:  cfg.Proxy.AllowedOrigins,
		UpstreamTimeout: cfg.Proxy.UpstreamTimeout,
		MaxBodyBytes:    cfg.Proxy.MaxBodyBytes,
	})
	mediaServiceRules := map[string][]string{}
	for service, serviceCfg := range cfg.Media.Services {
		mediaServiceRules[service] = serviceCfg.AccessRules
	}

	server := &relay.Server{
		PublicRelayURL:  cfg.PublicRelayURL,
		Upstream:        relay.Upstream{URL: cfg.UpstreamRelayURL, Lookup: cfg.UpstreamTimeout, ProfileRelays: cfg.AdditionalRelays},
		Cache:           cache,
		NIP05:           nip05.Client{BaseURL: cfg.TrustrootsNIP05Base},
		AuthCacheTTL:    cfg.AuthCacheTTL,
		AuthEventMaxAge: cfg.AuthEventMaxAge,
		AdminAuth:       admin.NewAuthorizer(cfg.AdminPubkeys, cfg.AdminAuthMaxAge),
		MediaGateway: media.Gateway{
			ConnectorBaseURL:   cfg.MediaConnectorURL,
			ConnectorToken:     cfg.MediaConnectorToken,
			TransportLabel:     cfg.MediaTransportLabel,
			Auth:               media.NewAuthorizer(cfg.MediaGrantPubkeys, cfg.MediaAuthMaxAge),
			Access:             accessAuth,
			ServiceAccessRules: mediaServiceRules,
			HTTPClient:         &http.Client{Timeout: cfg.MediaHTTPTimeout},
		},
		GenericProxy: genericProxy,
	}

	log.Printf("wrapster listening on %s, relay upstream %s", cfg.ListenAddr, cfg.UpstreamRelayURL)
	for platform, target := range cfg.Proxy.Targets {
		log.Printf("/proxy/%s -> %s", platform, target)
	}
	if cfg.Proxy.DefaultTarget != "" {
		log.Printf("/proxy fallback -> %s", cfg.Proxy.DefaultTarget)
	}
	if err := http.ListenAndServe(cfg.ListenAddr, server); err != nil {
		log.Fatal(err)
	}
}
