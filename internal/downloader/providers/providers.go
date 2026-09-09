package providers

import (
	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/facebook"
	"rest-api/internal/downloader/providers/instagram"
	"rest-api/internal/downloader/providers/ninexbuddy"
	"rest-api/internal/downloader/providers/savefrom"
	"rest-api/internal/downloader/providers/shopee"
	"rest-api/internal/downloader/providers/tiktok"
	"rest-api/internal/downloader/providers/youtube"
)

func RegisterAll(registry *downloader.Registry, opts ...Options) error {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	for _, p := range []downloader.Provider{
		facebook.New(),
		instagram.NewWithConfig(instagram.Config{InstagramCookie: o.InstagramCookie}),
		tiktok.New(),
		shopee.New(),
		// YouTube claims its own URLs so they are routed to the dedicated
		// /v1/youtube/* endpoints instead of the generic fallback below.
		youtube.New(),
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

// Options configures optional provider behaviour that is controlled through
// environment variables rather than code.
type Options struct {
	// InstagramCookie is a logged-in Instagram session cookie used to unlock
	// the official GraphQL path for full metadata.
	InstagramCookie string
}
