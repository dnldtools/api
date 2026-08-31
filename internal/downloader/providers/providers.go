package providers

import (
	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/facebook"
	"rest-api/internal/downloader/providers/instagram"
	"rest-api/internal/downloader/providers/tiktok"
)

func RegisterAll(registry *downloader.Registry) error {
	for _, p := range []downloader.Provider{
		facebook.New(),
		instagram.New(),
		tiktok.New(),
	} {
		if err := registry.Register(p); err != nil {
			return err
		}
	}
	return nil
}
