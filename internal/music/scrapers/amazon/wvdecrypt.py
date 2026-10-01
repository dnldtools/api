#!/usr/bin/env python3
import argparse
import base64
import html
import json
import os
import subprocess
import sys
import tempfile

import requests

try:
    from pywidevine.cdm import Cdm
    from pywidevine.device import Device
    from pywidevine.pssh import PSSH
except ImportError:
    sys.exit("missing pywidevine: pip install pywidevine")

KUKI_FILE = os.environ.get("AMAZON_KUKI") or os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "..", "..", "cookie", "amazon.json")
GQL = "https://gql.music.amazon.dev"
CONFIG = "https://music.amazon.com/config.json"
ORIGIN = "https://music.amazon.com"
UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
FIREFLY_AUTH_KEY = "amzn1.application.e1dc16675f9f4c78b31927d5bfd5c229"
QUALITY_CAP = {"std": "STD_RES", "hifi": "HI_FI", "hires": "HI_RES"}
MUSIC_AGENT = "Chrome/124.0.0.0 AmazonMusic/1.0"


def load_cookie():
    with open(KUKI_FILE, "r", encoding="utf-8") as f:
        kuki = json.load(f)
    if isinstance(kuki, list):
        return "; ".join(
            f"{c['name']}={c['value']}"
            for c in kuki
            if c and c.get("value") and c.get("domain", "").endswith("amazon.com")
        )
    return "; ".join(
        f"{name}={meta['value']}"
        for name, meta in kuki.items()
        if meta and meta.get("value") and meta.get("domain", "").endswith("amazon.com")
    )


def get_config(cookie):
    r = requests.post(
        f"{CONFIG}?clientApplication=skyfire&skipToken=false",
        headers={
            "user-agent": UA,
            "origin": ORIGIN,
            "referer": f"{ORIGIN}/",
            "cookie": cookie,
        },
        timeout=30,
    )
    r.raise_for_status()
    return r.json()


def get_stream(cfg, asin, capability):
    auth_token = base64.b64encode(
        json.dumps({"access_token": cfg["accessToken"]}).encode()
    ).decode()
    headers = {
        "user-agent": UA,
        "content-type": "application/json",
        "origin": ORIGIN,
        "x-api-key": FIREFLY_AUTH_KEY,
        "music-territory": "US",
        "x-amzn-device-id": cfg["deviceId"],
        "x-amzn-device-type": "A16ZV8BU3SN1N3",
        "x-amzn-session-id": cfg["sessionId"],
        "authorization": f"AmznMusic {auth_token}",
    }
    query = (
        'query($id: String!) { track(id: $id) { id title duration '
        f'playbackInformation(drmType: "WIDEVINE", audioCapability: "{capability}") '
        '{ audioUrl format protocol expireAt licenseHeaders { name value } } } }'
    )
    r = requests.post(
        GQL,
        headers=headers,
        json={"query": query, "variables": {"id": asin}},
        timeout=30,
    )
    r.raise_for_status()
    data = r.json()
    if data.get("errors"):
        raise RuntimeError(json.dumps(data["errors"]))
    return data["data"]["track"]


def parse_mpd(mpd):
    import re

    def grab(pattern, group=1):
        m = re.search(pattern, mpd)
        return m.group(group) if m else None

    base_url = grab(r"<BaseURL>([^<]+)</BaseURL>")
    return {
        "pssh": grab(r"<cenc:pssh>([^<]+)</cenc:pssh>"),
        "kid": grab(r'cenc:default_KID="([^"]+)"'),
        "license_url": grab(r"<amz:LicenseUrl>([^<]+)</amz:LicenseUrl>"),
        "base_url": html.unescape(base_url) if base_url else None,
        "codecs": grab(r'codecs="([^"]+)"'),
        "sampling_rate": grab(r'audioSamplingRate="([0-9]+)"'),
        "bit_depth": grab(r'amz-music:bitDepth" value="([^"]+)"'),
        "stream_name": grab(r'tag:amazon\.com,2019:dash:StreamName" value="([^"]+)"'),
    }


def post_license(cfg, cookie, license_url, license_headers, challenge_b64):
    headers = dict(license_headers)
    headers.update(
        {
            "content-type": "application/json",
            "user-agent": MUSIC_AGENT,
            "x-amz-music-agent": MUSIC_AGENT,
            "cookie": cookie,
            "csrf-token": cfg["csrf"]["token"],
            "csrf-ts": str(cfg["csrf"]["ts"]),
            "csrf-rnd": str(cfg["csrf"]["rnd"]),
        }
    )
    if cfg.get("accessToken"):
        headers["Authorization"] = f"Bearer {cfg['accessToken']}"
    r = requests.post(
        license_url,
        headers=headers,
        json={"licenseChallenge": challenge_b64},
        timeout=30,
    )
    r.raise_for_status()
    return r


def extract_license_bytes(resp):
    ctype = resp.headers.get("content-type", "")
    if "json" in ctype:
        data = resp.json()
        if isinstance(data, dict):
            for key in ("license", "licenseResponse", "licensePayload", "response", "licenseChallenge"):
                if key in data and data[key]:
                    try:
                        return base64.b64decode(data[key])
                    except Exception:
                        pass
            if "license" in data:
                return data["license"].encode() if isinstance(data["license"], str) else data["license"]
        return resp.content
    return resp.content


def decrypt_file(keys, mp4_path, out_path):
    kid_key = ":".join(f"{k.kid.hex}={k.key.hex()}" for k in keys)
    cmd = [
        "ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
        "-decryption_keys", kid_key,
        "-i", mp4_path,
        "-c", "copy",
        out_path,
    ]
    subprocess.run(cmd, check=True)


def stream_decrypt(keys, mp4_url, codecs):
    kid_key = ":".join(f"{k.kid.hex}={k.key.hex()}" for k in keys)
    fmt = "flac" if (codecs or "").startswith("flac") else "opus"
    cmd = [
        "ffmpeg", "-hide_banner", "-loglevel", "error",
        "-decryption_keys", kid_key,
        "-i", mp4_url,
        "-c", "copy",
        "-f", fmt, "pipe:1",
    ]
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    try:
        while True:
            chunk = proc.stdout.read(1024 * 1024)
            if not chunk:
                break
            sys.stdout.buffer.write(chunk)
            sys.stdout.buffer.flush()
    finally:
        proc.stdout.close()
        err_txt = proc.stderr.read().decode(errors="replace")
        proc.wait()
        if proc.returncode != 0:
            sys.exit(f"ffmpeg failed ({proc.returncode}): {err_txt[:300]}")


def tag_file(in_path, args):
    ext = os.path.splitext(in_path)[1]
    cover = None
    if args.artwork:
        cover = in_path + ".cover.jpg"
        try:
            with requests.get(args.artwork, stream=True, timeout=60) as r:
                r.raise_for_status()
                with open(cover, "wb") as f:
                    for chunk in r.iter_content(1024 * 1024):
                        f.write(chunk)
        except Exception:
            cover = None
    cmd = ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", in_path]
    embed_cover = cover and os.path.exists(cover) and ext in (".flac", ".m4a", ".mp4")
    if embed_cover:
        cmd += ["-i", cover, "-map", "0:a", "-map", "1:v", "-c:v", "copy", "-disposition:v:0", "attached_pic"]
    else:
        cmd += ["-map", "0:a"]
    cmd += ["-c:a", "copy"]
    if ext in (".m4a", ".mp4"):
        cmd += ["-movflags", "+faststart"]

    def set_(k, v):
        if v not in (None, ""):
            cmd.extend(["-metadata", f"{k}={v}"])

    set_("title", args.title)
    set_("artist", args.artist)
    set_("album_artist", args.artist)
    set_("album", args.album)
    set_("date", args.year)
    set_("track", args.track)
    set_("disc", args.disc if (args.disc or 0) > 1 else None)
    set_("isrc", args.isrc)
    set_("copyright", args.copyright)
    set_("comment", f"amazon music {args.asin}")

    tmp = in_path + ".tagging" + ext
    cmd += [tmp]
    subprocess.run(cmd, check=True)
    if cover and os.path.exists(cover):
        os.unlink(cover)
    os.replace(tmp, in_path)


def main():
    ap = argparse.ArgumentParser(description="Amazon Music Widevine full-track downloader")
    ap.add_argument("asin", help="track ASIN, e.g. B0H1VFLMXB")
    ap.add_argument("--device", required=True, help="pywidevine .wvd device file")
    ap.add_argument("--quality", choices=["std", "hifi", "hires"], default="hires")
    ap.add_argument("--out", help="output file (default: <asin>_<quality>.<flac|opus>)")
    ap.add_argument("--title")
    ap.add_argument("--artist")
    ap.add_argument("--album")
    ap.add_argument("--track")
    ap.add_argument("--disc")
    ap.add_argument("--year")
    ap.add_argument("--isrc")
    ap.add_argument("--copyright")
    ap.add_argument("--artwork", help="cover image URL")
    args = ap.parse_args()

    if not shutil_which("ffmpeg"):
        sys.exit("missing ffmpeg on PATH")

    cookie = load_cookie()
    cfg = get_config(cookie)
    capability = QUALITY_CAP[args.quality]
    track = get_stream(cfg, args.asin, capability)
    pi = track["playbackInformation"]
    if not pi or not pi.get("audioUrl"):
        sys.exit("no audioUrl: premium FireFly auth failed")

    license_headers = {h["name"]: h["value"] for h in pi.get("licenseHeaders", [])}
    mpd = requests.get(pi["audioUrl"], timeout=30).text
    manifest = parse_mpd(mpd)
    if not manifest["pssh"] or not manifest["license_url"]:
        sys.exit("MPD missing pssh/licenseUrl")

    device = Device.load(args.device)
    cdm = Cdm.from_device(device)
    session_id = cdm.open()
    try:
        challenge = cdm.get_license_challenge(session_id, PSSH(manifest["pssh"]))
        resp = post_license(
            cfg,
            cookie,
            manifest["license_url"],
            license_headers,
            base64.b64encode(challenge).decode(),
        )
        license_bytes = extract_license_bytes(resp)
        if not license_bytes:
            sys.exit(f"empty license response: {resp.status} {resp.content[:200]}")
        cdm.parse_license(session_id, license_bytes)
        keys = cdm.get_keys(session_id)
        if not keys:
            sys.exit("no content keys in license")
    finally:
        cdm.close(session_id)

    ext = "flac" if (manifest.get("codecs") or "").startswith("flac") else "opus"
    if args.out == "-":
        print(f"[+] keys ({len(keys)}):", file=sys.stderr)
        for k in keys:
            print(f"    {k.kid.hex}={k.key.hex()} ({k.type})", file=sys.stderr)
        print(f"[+] streaming CDN -> ffmpeg -> stdout: {manifest['base_url'][:80]}...", file=sys.stderr)
        stream_decrypt(keys, manifest["base_url"], manifest["codecs"])
        return
    print(f"[+] keys ({len(keys)}):")
    for k in keys:
        print(f"    {k.kid.hex}={k.key.hex()} ({k.type})")
    out_path = args.out or f"{args.asin}_{args.quality}.{ext}"
    print(f"[+] downloading encrypted mp4: {manifest['base_url'][:80]}...")
    tmp = tempfile.NamedTemporaryFile(suffix=".mp4", delete=False)
    tmp.close()
    with requests.get(manifest["base_url"], stream=True, timeout=120) as r:
        r.raise_for_status()
        with open(tmp.name, "wb") as f:
            for chunk in r.iter_content(1024 * 1024):
                f.write(chunk)
    try:
        print("[+] decrypting with ffmpeg...")
        decrypt_file(keys, tmp.name, out_path)
        print(f"[+] saved {out_path}")
    finally:
        os.unlink(tmp.name)
    if any([args.title, args.artist, args.album, args.artwork]):
        print("[+] injecting tags...")
        tag_file(out_path, args)


def shutil_which(name):
    from shutil import which

    return which(name)


if __name__ == "__main__":
    main()
