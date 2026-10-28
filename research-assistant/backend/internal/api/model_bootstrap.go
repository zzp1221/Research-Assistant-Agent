package api

import (
	"log"

	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"
)

// bootstrapRuntimeModels installs the process-wide model profile store and
// resolver, and seeds the env-derived global profile when the store is empty.
// It must run before agents are constructed so their dynamic proxies can resolve
// profiles on the first request.
func bootstrapRuntimeModels() *modelresolver.Resolver {
	dir := modelresolver.DefaultStoreDir()
	store, err := modelprofile.NewStore(dir)
	if err != nil {
		log.Printf("[model-profile] store init failed (%s): %v; falling back to env-only models", dir, err)
		store, _ = modelprofile.NewStore("")
	}
	if store != nil && modelprofile.EncryptionAvailable() {
		if cfg, cfgErr := appconfig.LoadLLMConfig(); cfgErr == nil {
			if _, bootErr := store.UpsertBootstrap(cfg.BaseURL, cfg.Model, cfg.APIKey); bootErr != nil {
				log.Printf("[model-profile] bootstrap failed: %v", bootErr)
			}
		}
	}
	resolver := modelresolver.New(store)
	modelresolver.SetDefault(resolver)
	return resolver
}
