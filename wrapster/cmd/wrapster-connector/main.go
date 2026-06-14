package main

import (
	"log"
	"net/http"

	"github.com/trustroots/nostroots/vibe/wrapster/internal/admin"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/config"
	"github.com/trustroots/nostroots/vibe/wrapster/internal/media"
)

func main() {
	cfg, err := config.LoadConnector()
	if err != nil {
		log.Fatal(err)
	}
	allowedCIDRs, err := media.ParseCIDRs(cfg.AllowedCIDRs)
	if err != nil {
		log.Fatal(err)
	}

	mediaCfg := media.ConnectorMediaConfig{
		JellyfinBaseURL: cfg.JellyfinBaseURL,
		JellyfinAPIKey:  cfg.JellyfinAPIKey,
		PlexBaseURL:     cfg.PlexBaseURL,
		PlexToken:       cfg.PlexToken,
	}
	if fileCfg, ok, err := media.LoadConnectorMediaConfig(cfg.ConfigPath); err != nil {
		log.Fatal(err)
	} else if ok {
		mediaCfg = fileCfg
	}

	connector := &media.Connector{
		AllowedCIDRs: allowedCIDRs,
		SharedToken:  cfg.SharedToken,
		HTTPClient:   &http.Client{Timeout: cfg.HTTPTimeout},
	}
	connector.SetMediaConfig(mediaCfg)

	errs := make(chan error, 2)
	log.Printf("wrapster connector API listening on %s", cfg.ListenAddr)
	go func() {
		errs <- http.ListenAndServe(cfg.ListenAddr, connector)
	}()

	if cfg.SetupListenAddr != "" {
		setup := media.SetupHandler{
			Connector:    connector,
			ConfigPath:   cfg.ConfigPath,
			FIPSNsecPath: cfg.FIPSNsecPath,
			Auth:         admin.NewAuthorizer(cfg.SetupAdminPubkeys, cfg.SetupAuthMaxAge),
		}
		log.Printf("wrapster connector setup UI listening on %s", cfg.SetupListenAddr)
		go func() {
			errs <- http.ListenAndServe(cfg.SetupListenAddr, setup)
		}()
	}

	if err := <-errs; err != nil {
		log.Fatal(err)
	}
}
