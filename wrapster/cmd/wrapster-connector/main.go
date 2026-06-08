package main

import (
	"log"
	"net/http"

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

	connector := media.Connector{
		AllowedCIDRs:    allowedCIDRs,
		SharedToken:     cfg.SharedToken,
		JellyfinBaseURL: cfg.JellyfinBaseURL,
		JellyfinAPIKey:  cfg.JellyfinAPIKey,
		PlexBaseURL:     cfg.PlexBaseURL,
		PlexToken:       cfg.PlexToken,
		HTTPClient:      &http.Client{Timeout: cfg.HTTPTimeout},
	}

	log.Printf("wrapster connector listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, connector); err != nil {
		log.Fatal(err)
	}
}
