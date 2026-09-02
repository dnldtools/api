package providers

import (
	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/facebook"
	"rest-api/internal/downloader/providers/instagram"
	"rest-api/internal/downloader/providers/ninexbuddy"
	"rest-api/internal/downloader/providers/savefrom"
	"rest-api/internal/downloader/providers/shopee"
	"rest-api/internal/downloader/providers/tiktok"
)

func RegisterAll(registry *downloader.Registry) error {
	for _, p := range []downloader.Provider{
		facebook.New(),
		instagram.New(),
		tiktok.New(),
		shopee.New(),
		// 9xbuddy is an all-in-one fallback: registered last so dedicated
		// providers above still win for their own URLs.
		ninexbuddy.New(),
		// savefrom.net is another all-in-one fallback, registered after 9xbuddy
		// so 9xbuddy remains the default for auto-detected generic URLs.
		// savefrom is still reachable via an explicit platform hint.
		savefrom.New(),
	} {
		if err := registry.Register(p); err != nil {
			return err
		}
	}
	return nil
}
