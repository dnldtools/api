#!/usr/bin/env python3
import argparse
import base64
import json
import os
import re
import subprocess
import sys
from shutil import which
from uuid import UUID

import requests

try:
    from pywidevine.cdm import Cdm
    from pywidevine.device import Device
    from pywidevine.pssh import PSSH
except ImportError:
    sys.exit("missing pywidevine: pip install pywidevine")

WV_SYSTEM_ID = UUID("edef8ba9-79d6-4ace-a3c8-27dcd51d21ed")
AMP = "https://amp-api.music.apple.com/v1/catalog"
WEBPLAYBACK = "https://play.music.apple.com/WebObjects/MZPlay.woa/wa/webPlayback"
ORIGIN = "https://music.apple.com"
UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"

DEV_TOKEN = os.environ.get(
    "APPLE_DEV_TOKEN",
    "eyJ0eXAiOiJKV1QiLCJhbGciOiJFUzI1NiIsImtpZCI6IldlYlBsYXlLaWQifQ.eyJpc3MiOiJBTVBXZWJQbGF5IiwiaWF0IjoxNzg5Njg4NzA5LCJleHAiOjE3OTU3MzY3MDksInJvb3RfaHR0cHNfb3JpZ2luIjpbImFwcGxlLmNvbSJdfQ.y0gd6YWyrUrZx-YZNZS0xVHkDHGr-kGZ9RrsWRfApGc2-_NNC968VsD36hRU33s5BBs4KdB7LIZTmYqPra097Q",
)
MUT = os.environ.get("APPLE_MUSIC_USER_TOKEN", "")
FLAVOR_RANK = ["28:ctrp256", "30:cbcp256", "32:ctrp64", "34:cbcp64", "37:ibhp256", "38:ibhp64"]


def web_headers(content_type=False):
    h = {
        "authorization": f"Bearer {DEV_TOKEN}",
        "media-user-token": MUT,
        "origin": ORIGIN,
        "referer": f"{ORIGIN}/",
        "user-agent": UA,
    }
    if content_type:
        h["content-type"] = "application/json"
    return h


def get_playback(track_id):
    r = requests.post(
        WEBPLAYBACK,
        headers=web_headers(True),
        data=json.dumps({"salableAdamId": str(track_id)}),
        timeout=30,
    )
    r.raise_for_status()
    j = r.json()
    songs = j.get("songList") or []
    if not songs:
        raise RuntimeError(f"empty songList: {json.dumps(j)[:200]}")
    song = songs[0]
    if "failureType" in song:
        raise RuntimeError(f"playback failure: {json.dumps(song)[:300]}")
    return song


def pick_asset(song, flavor=None):
    assets = song.get("assets") or []
    if not assets:
        raise RuntimeError("no assets in playback response")
    if flavor:
        for a in assets:
            if a.get("flavor") == flavor:
                return a
    for ranked in FLAVOR_RANK:
        for a in assets:
            if a.get("flavor") == ranked:
                return a
    return assets[0]


def parse_m3u8(m3u8_text, m3u8_url):
    m = re.search(r'URI="data:;base64,([^"]+)"', m3u8_text)
    if not m:
        raise RuntimeError("no KID in m3u8 (expected data:;base64,<kid>)")
    kid = base64.b64decode(m.group(1))
    mp4_name = None
    mm = re.search(r'#EXT-X-MAP:URI="([^"]+)"', m3u8_text)
    if mm:
        mp4_name = mm.group(1)
    else:
        for line in m3u8_text.splitlines():
            if line and not line.startswith("#"):
                mp4_name = line.strip()
                break
    if not mp4_name:
        raise RuntimeError("no segment uri in m3u8")
    base = m3u8_url.rsplit("/", 1)[0] + "/"
    return kid, base + mp4_name


def fetch_content_keys(song, track_id, kid, device_path):
    license_url = song["hls-key-server-url"]
    device = Device.load(device_path)
    cdm = Cdm.from_device(device)
    session_id = cdm.open()
    try:
        pssh = PSSH.new(system_id=WV_SYSTEM_ID, key_ids=[kid])
        challenge = cdm.get_license_challenge(session_id, pssh)
        body = {
            "challenge": base64.b64encode(challenge).decode(),
            "key-system": "com.widevine.alpha",
            "uri": "data:;base64," + base64.b64encode(kid).decode(),
            "adamId": str(track_id),
            "isLibrary": False,
            "user-initiated": True,
        }
        r = requests.post(license_url, headers=web_headers(True), data=json.dumps(body), timeout=30)
        r.raise_for_status()
        lj = r.json()
        if "license" not in lj:
            raise RuntimeError(f"no license field: {json.dumps(lj)[:300]}")
        cdm.parse_license(session_id, lj["license"])
        keys = [k for k in cdm.get_keys(session_id) if k.type == "CONTENT"]
        if not keys:
            raise RuntimeError("no CONTENT keys in license")
        return keys
    finally:
        cdm.close(session_id)


def download(url, path):
    with requests.get(url, stream=True, timeout=120) as r:
        r.raise_for_status()
        with open(path, "wb") as f:
            for chunk in r.iter_content(1024 * 1024):
                f.write(chunk)


def decrypt(keys, enc_path, out_path):
    if len(keys) == 1:
        dec_args = ["-decryption_key", keys[0].key.hex()]
    else:
        dec_args = ["-decryption_keys", ":".join(f"{k.kid.hex}={k.key.hex()}" for k in keys)]
    proc = subprocess.run(
        [
            "ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
            *dec_args,
            "-i", enc_path,
            "-c", "copy",
            out_path,
        ],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"ffmpeg exit {proc.returncode}: {(proc.stderr or '').strip()[:300]}")


def artwork_url(attrs):
    art = (attrs or {}).get("artwork", {})
    url = art.get("url")
    if not url:
        return None
    return re.sub(r"(?:\{w\}x\{h\}|%7Bw%7Dx%7Bh%7D|\d+x\d+)bb", "3000x3000bb", url, flags=re.IGNORECASE)


def get_metadata(track_id, sf):
    r = requests.get(
        f"{AMP}/{sf}/songs/{track_id}",
        headers=web_headers(),
        params={"l": "en-US"},
        timeout=30,
    )
    r.raise_for_status()
    data = r.json()["data"][0]
    a = data["attributes"]
    return {
        "artist": a.get("artistName", ""),
        "title": a.get("name", ""),
        "album": a.get("albumName", ""),
        "track": a.get("trackNumber"),
        "disc": a.get("discNumber"),
        "year": (a.get("releaseDate") or "")[:4] or None,
        "isrc": a.get("isrc"),
        "genre": (a.get("genreNames") or [None])[0],
        "copyright": a.get("copyright"),
        "artwork": artwork_url(a),
    }


def sanitize(name):
    return re.sub(r'[\\/:*?"<>|]+', "_", name).strip(" .")


def tag_file(in_path, meta, track_id, artwork_path):
    args = ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", in_path]
    if artwork_path:
        args += ["-i", artwork_path, "-map", "0:a", "-map", "1:v", "-c:v", "copy", "-disposition:v:0", "attached_pic"]
    else:
        args += ["-map", "0:a"]
    args += ["-c:a", "copy", "-movflags", "+faststart"]

    def set_(k, v):
        if v not in (None, ""):
            args.extend(["-metadata", f"{k}={v}"])

    set_("title", meta.get("title"))
    set_("artist", meta.get("artist"))
    set_("album_artist", meta.get("artist"))
    set_("album", meta.get("album"))
    set_("date", meta.get("year"))
    set_("track", meta.get("track"))
    set_("disc", meta.get("disc") if (meta.get("disc") or 0) > 1 else None)
    set_("isrc", meta.get("isrc"))
    set_("genre", meta.get("genre"))
    set_("copyright", meta.get("copyright"))
    set_("comment", f"apple music {track_id}")

    tmp = in_path + ".tagging.m4a"
    args += [tmp]
    subprocess.run(args, check=True)
    os.replace(tmp, in_path)


def write_mp4_isrc(path, meta):
    try:
        from mutagen.mp4 import MP4, MP4FreeForm
    except ImportError:
        return
    try:
        audio = MP4(path)
        isrc = meta.get("isrc")
        if isrc:
            audio["----:com.apple.iTunes:ISRC"] = [MP4FreeForm(str(isrc).encode())]
        if meta.get("copyright"):
            audio["cprt"] = [str(meta["copyright"])]
        audio.save()
    except Exception as e:
        print(f"[-] mp4 tag warning: {e}", file=sys.stderr)


def bitrate_kbps(value):
    try:
        n = int(value)
    except (TypeError, ValueError):
        return 0
    if n <= 0:
        return 0
    return round(n / 1000) if n > 10000 else n


def process_asset(song, asset, track_id, device_path, out_prefix, sf, meta, tag=True):
    flavor = asset.get("flavor") or "unknown"
    kbps = bitrate_kbps((asset.get("metadata") or {}).get("bitRate"))
    m3u8_url = asset["URL"]
    m3u8_text = requests.get(m3u8_url, timeout=30).text
    kid, mp4_url = parse_m3u8(m3u8_text, m3u8_url)
    keys = fetch_content_keys(song, track_id, kid, device_path)

    slug = str(kbps) if kbps else flavor.replace(":", "-")
    out_path = f"{out_prefix}.{slug}.m4a"
    with tempfile_path() as enc_path:
        download(mp4_url, enc_path)
        decrypt(keys, enc_path, out_path)

    if tag and meta:
        cover = None
        try:
            if meta.get("artwork"):
                cover = os.path.join(os.path.dirname(os.path.abspath(out_path)) or ".", ".apple_cover.jpg")
                download(meta["artwork"], cover)
            tag_file(out_path, meta, track_id, cover)
            write_mp4_isrc(out_path, meta)
        finally:
            if cover and os.path.exists(cover):
                os.unlink(cover)

    label = f"AAC \u00b7 {kbps} kbps" if kbps else f"AAC \u00b7 {flavor}"
    return {
        "path": out_path,
        "ext": "m4a",
        "quality": str(kbps) if kbps else flavor,
        "codec": "mp4a.40.2",
        "bitrate": kbps * 1000,
        "label": label,
    }


def process_track_all(track_id, device_path, out_prefix, sf, flavor=None, tag=True):
    song = get_playback(track_id)
    assets = song.get("assets") or []
    if flavor:
        assets = [a for a in assets if a.get("flavor") == flavor]
    if not assets:
        raise RuntimeError("no assets in playback response")

    meta = get_metadata(track_id, sf) if tag else {}
    if not out_prefix:
        base = f"{sanitize(meta.get('artist', ''))} - {sanitize(meta.get('title', ''))}" if tag else str(track_id)
        out_prefix = base or str(track_id)

    files = []
    seen = set()
    for asset in assets:
        asset_flavor = asset.get("flavor") or "unknown"
        kbps = bitrate_kbps((asset.get("metadata") or {}).get("bitRate"))
        key = kbps or asset_flavor
        if key in seen:
            continue
        seen.add(key)
        try:
            files.append(process_asset(song, asset, track_id, device_path, out_prefix, sf, meta, tag))
        except Exception as e:
            print(f"[-] flavor {asset_flavor} failed: {e}", file=sys.stderr)

    if not files:
        raise RuntimeError("all flavors failed")
    return files


def tempfile_path():
    import tempfile

    f = tempfile.NamedTemporaryFile(suffix=".mp4", delete=False)
    f.close()
    return _TempPath(f.name)


class _TempPath:
    def __init__(self, name):
        self.name = name

    def __enter__(self):
        return self.name

    def __exit__(self, *a):
        try:
            os.unlink(self.name)
        except OSError:
            pass


def list_tracks(kind, item_id, sf):
    q = f"{AMP}/{sf}/{kind}/{item_id}"
    r = requests.get(q, headers=web_headers(), params={"l": "en-US", "include": "tracks"}, timeout=30)
    r.raise_for_status()
    rel = r.json()["data"][0].get("relationships", {}).get("tracks", {})
    return [t["id"] for t in rel.get("data", [])]


def main():
    ap = argparse.ArgumentParser(description="Apple Music web-player Widevine full-track downloader")
    ap.add_argument("id", nargs="?", help="song/adamId, or use --album/--playlist")
    ap.add_argument("--device", required=True, help="pywidevine .wvd device file")
    ap.add_argument("--out", help="output prefix (default: 'Artist - Title')")
    ap.add_argument("--sf", default="us", help="storefront (default us)")
    ap.add_argument("--flavor", help="only download one asset flavor, e.g. 28:ctrp256")
    ap.add_argument("--album", metavar="ID", help="download every track of an album id")
    ap.add_argument("--playlist", metavar="ID", help="download every track of a playlist id")
    ap.add_argument("--no-tag", dest="tag", action="store_false", help="skip metadata lookup, name output <id>.<n>.m4a")
    args = ap.parse_args()

    if not which("ffmpeg"):
        sys.exit("missing ffmpeg on PATH")

    if args.album:
        tracks = list_tracks("albums", args.album, args.sf)
        print(f"[+] album {args.album}: {len(tracks)} tracks", file=sys.stderr)
        batch = True
    elif args.playlist:
        tracks = list_tracks("playlists", args.playlist, args.sf)
        print(f"[+] playlist {args.playlist}: {len(tracks)} tracks", file=sys.stderr)
        batch = True
    else:
        if not args.id:
            ap.error("required: id or --album/--playlist")
        tracks = [args.id]
        batch = False

    results = []
    for i, tid in enumerate(tracks, 1):
        print(f"[+] ({i}/{len(tracks)}) track {tid}", file=sys.stderr)
        try:
            files = process_track_all(tid, args.device, args.out if not batch else None, args.sf, args.flavor, args.tag)
            results.append({"id": str(tid), "ok": True, "files": files})
        except Exception as e:
            print(f"[-] failed {tid}: {e}", file=sys.stderr)
            results.append({"id": str(tid), "ok": False, "error": str(e)})

    payload = {"ok": any(r["ok"] for r in results), "results": results}
    if len(results) == 1:
        payload["id"] = results[0]["id"]
        payload["files"] = results[0].get("files", [])
    print(json.dumps(payload))


if __name__ == "__main__":
    main()
