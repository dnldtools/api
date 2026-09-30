package music

import "testing"

func TestDetectPlatform(t *testing.T) {
	cases := []struct {
		url  string
		want Platform
	}{
		{"https://music.amazon.com/albums/B001O3B2M2", PlatformAmazon},
		{"https://music.amazon.co.jp/albums/B0ABC12345", PlatformAmazon},
		{"https://www.amazon.com/music/player/albums/B001O3B2M2", PlatformAmazon},
		{"https://music.apple.com/us/album/blinding-lights/1488408555?i=1488408568", PlatformApple},
		{"https://geo.music.apple.com/us/album/random-access-memories/617154241", PlatformApple},
		{"https://itunes.apple.com/us/album/1488408555", PlatformApple},
		{"https://soundcloud.com/glenhansen/kygo-firestone", PlatformSoundCloud},
		{"https://on.soundcloud.com/abc123", PlatformSoundCloud},
		{"https://soundcloud.app.goo.gl/xyz", PlatformSoundCloud},
		{"https://m.soundcloud.com/glenhansen/kygo-firestone", PlatformSoundCloud},
		{"https://tidal.com/browse/track/134858527", PlatformTidal},
		{"https://listen.tidal.com/album/134858516", PlatformTidal},
		{"https://example.com/x", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := DetectPlatform(tc.url); got != tc.want {
			t.Errorf("DetectPlatform(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestParseAppleURL(t *testing.T) {
	cases := []struct {
		url    string
		typ    string
		id     string
		sf     string
		songID string
	}{
		{"https://music.apple.com/us/album/blinding-lights/1488408555?i=1488408568", "album", "1488408555", "us", "1488408568"},
		{"https://music.apple.com/us/album/random-access-memories/617154241", "album", "617154241", "us", ""},
		{"https://geo.music.apple.com/us/song/blinding-lights/1488408568", "song", "1488408568", "us", ""},
		{"https://itunes.apple.com/de/playlist/top-hits/pl.abc123", "playlist", "pl.abc123", "de", ""},
		{"https://music.apple.com/us/album/name/id617154241", "album", "617154241", "us", ""},
		{"https://music.apple.com/us/artist/the-weeknd/479756766", "artist", "479756766", "us", ""},
	}
	for _, tc := range cases {
		typ, id, sf, songID := parseAppleURL(tc.url)
		if typ != tc.typ || id != tc.id || sf != tc.sf || songID != tc.songID {
			t.Errorf("parseAppleURL(%q) = (%q, %q, %q, %q), want (%q, %q, %q, %q)",
				tc.url, typ, id, sf, songID, tc.typ, tc.id, tc.sf, tc.songID)
		}
	}
}

func TestAbsPath(t *testing.T) {
	if got := absPath(""); got != "" {
		t.Errorf("absPath(\"\") = %q, want empty", got)
	}
	if got := absPath("  "); got != "" {
		t.Errorf("absPath(\"  \") = %q, want empty", got)
	}
	if got := absPath("cookie/tidal.json"); got == "cookie/tidal.json" {
		t.Errorf("absPath(\"cookie/tidal.json\") should be absolute, got %q", got)
	}
}
