package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers/facebook"
	"rest-api/internal/downloader/providers/instagram"
	"rest-api/internal/downloader/providers/tiktok"
)

type caseDef struct {
	platform downloader.Platform
	url      string
}

func main() {
	cases := []caseDef{
		{downloader.PlatformFacebook, "https://www.facebook.com/reel/2165661110964452"},
		{downloader.PlatformFacebook, "https://www.facebook.com/share/p/1BmZhmqzFP/"},
		{downloader.PlatformInstagram, "https://www.instagram.com/p/BsOGulcndj-/"},
		{downloader.PlatformInstagram, "https://www.instagram.com/p/C2kVYt6J_1V/"},
		{downloader.PlatformTikTok, "https://www.tiktok.com/@tiktok/video/7106594312292453675"},
	}

	envCookie := func(k string) string { return os.Getenv(k) }

	fbCfg := facebook.DefaultConfig()
	fbCfg.FacebookCookie = envCookie("FACEBOOK_COOKIE")
	fb := facebook.NewWithConfig(fbCfg)

	igCfg := instagram.DefaultConfig()
	igCfg.InstagramCookie = envCookie("INSTAGRAM_COOKIE")
	ig := instagram.NewWithConfig(igCfg)

	ttCfg := tiktok.DefaultConfig()
	ttCfg.TikTokCookie = envCookie("TIKTOK_COOKIE")
	tt := tiktok.NewWithConfig(ttCfg)

	ctx := context.Background()

	for _, c := range cases {
		var p downloader.Provider
		switch c.platform {
		case downloader.PlatformFacebook:
			p = fb
		case downloader.PlatformInstagram:
			p = ig
		case downloader.PlatformTikTok:
			p = tt
		}

		start := time.Now()
		res, err := p.Resolve(ctx, downloader.DownloadRequest{URL: c.url})
		elapsed := time.Since(start)

		fmt.Printf("\n===== %s | %s =====\n", c.platform, c.url)
		fmt.Printf("elapsed=%s\n", elapsed)
		if err != nil {
			fmt.Printf("ERROR: %v\n", err)
			continue
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Printf("%s\n", string(b))
	}
}
