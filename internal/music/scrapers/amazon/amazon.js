#!/usr/bin/env node
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { join } from 'node:path';

const GQL = 'https://gql.music.amazon.dev';
const TENZING = 'https://music.amazon.com/NA/api/textsearch/search/v1_1/';
const CONFIG = 'https://music.amazon.com/config.json';
const SAMPLE = 'https://music.amazon.com/getSampleTrack';
const ORIGIN = 'https://music.amazon.com';
const UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36';
const FIREFLY_WEB_KEY = 'amzn1.application.5d9d979e3a5f4e8ea83bc8536b4fde0b';
const FIREFLY_AUTH_KEY = 'amzn1.application.e1dc16675f9f4c78b31927d5bfd5c229';
const QUALITY_CAP = { std: 'STD_RES', hifi: 'HI_FI', hires: 'HI_RES' };
const DEFAULT_TERRITORY = 'US';
const DEFAULT_LOCALE = 'en_US';
const KUKI_FILE = process.env.AMAZON_KUKI || fileURLToPath(new URL('./kuki.json', import.meta.url));

const PREMIUM_COOKIE = (() => {
  try {
    return Object.entries(JSON.parse(readFileSync(KUKI_FILE, 'utf8')))
      .filter(([, v]) => v && /amazon\.com$/.test(v.domain))
      .map(([k, v]) => `${k}=${v.value}`)
      .join('; ');
  } catch { return ''; }
})();

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const err = (m) => { throw new Error(m); };
const flag = (argv, name, def) => { const i = argv.indexOf(name); return i === -1 ? def : argv[i + 1]; };
const sanitize = (s) => String(s).replace(/[\\/:*?"<>|\x00-\x1f]/g, '_').slice(0, 120);
const asinFromUrl = (u) => { try { const x = new URL(u); const seg = x.pathname.match(/\/(albums|artists|playlists|dp|tracks)\/(B0[A-Z0-9]{8})/i); if (seg) return { asin: seg[2], kind: seg[1].toLowerCase() }; if (x.searchParams.get('trackAsin')) return { asin: x.searchParams.get('trackAsin'), kind: 'track' }; return { asin: String(u).match(/B0[A-Z0-9]{8}/i)?.[0], kind: 'album' }; } catch { return { asin: String(u).match(/B0[A-Z0-9]{8}/i)?.[0], kind: 'album' }; } };

function runCapture(cmd, args) {
  return new Promise((resolve) => {
    let out = '', errOut = '';
    const child = spawn(cmd, args, { stdio: ['ignore', 'pipe', 'pipe'] });
    child.stdout.on('data', (d) => { out += d; });
    child.stderr.on('data', (d) => { errOut += d; });
    child.on('error', (e) => resolve({ ok: false, error: e.message, out, errOut }));
    child.on('exit', (code) => resolve({ ok: code === 0, code, out, errOut }));
  });
}

function metaArgs(t, albumMeta = {}) {
  const artist = (t.artists && t.artists[0] && t.artists[0].name) || (albumMeta.artists && albumMeta.artists[0] && albumMeta.artists[0].name) || null;
  const cover = (albumMeta.artwork && albumMeta.artwork[0] && albumMeta.artwork[0].url) || (t.artwork && t.artwork[0] && t.artwork[0].url) || null;
  const args = [];
  const push = (k, v) => { if (v != null && v !== '') args.push(k, String(v)); };
  push('--title', t.title);
  push('--artist', artist);
  push('--album', albumMeta.title || (t.album && t.album.title) || null);
  push('--track', t.trackNumber);
  push('--disc', t.discNumber);
  push('--year', albumMeta.year);
  push('--copyright', albumMeta.copyright);
  push('--artwork', cover);
  push('--isrc', t.isrc);
  return args;
}

function extForCodec(codecs) {
  const c = String(codecs || '').toLowerCase();
  if (c.includes('flac')) return 'flac';
  if (c.includes('mp4a') || c.includes('aac')) return 'm4a';
  if (c.includes('opus')) return 'opus';
  if (c.includes('mp3') || c.includes('mpeg')) return 'mp3';
  return 'm4a';
}

function qualityLabel(q, manifest) {
  const ext = extForCodec(manifest?.codecs);
  const kind = ext === 'flac' ? 'FLAC' : ext === 'opus' ? 'OPUS' : ext === 'mp3' ? 'MP3' : 'AAC';
  const tier = { hires: 'HI_RES', hifi: 'HI_FI', std: 'STD' }[q] || String(q).toUpperCase();
  const spec = [manifest?.bitDepth ? `${manifest.bitDepth}-bit` : null, manifest?.samplingRate ? `${manifest.samplingRate / 1000}kHz` : null].filter(Boolean).join('/');
  const head = [kind, tier].filter(Boolean).join(' · ');
  return spec ? `${head} (${spec})` : head;
}

async function downloadTracks(tracks, argv, albumMeta = {}) {
  const device = flag(argv, '--device', fileURLToPath(new URL('./device.wvd', import.meta.url)));
  const outdir = flag(argv, '--outdir', '.');
  const py = fileURLToPath(new URL('./wvdecrypt.py', import.meta.url));
  const results = [];
  for (const t of tracks) {
    const meta = metaArgs(t, albumMeta);
    const made = new Set();
    const files = [];
    for (const [q, defaultExt] of [['hires', 'flac'], ['hifi', 'flac'], ['std', 'opus']]) {
      let manifest;
      try { manifest = (await getStream(t.id, { quality: q }))?.manifest; }
      catch { continue; }
      if (!manifest?.pssh || !manifest?.licenseUrl || !manifest?.baseUrl) continue;
      const ext = extForCodec(manifest.codecs) || defaultExt;
      let file = join(outdir, `${sanitize(t.title)}.${ext}`);
      if (made.has(file)) file = join(outdir, `${sanitize(t.title)}.${q}.${ext}`);
      made.add(file);
      const r = await runCapture('python', [py, t.id, '--device', device, '--quality', q, '--out', file, ...meta]);
      if (!r.ok) continue;
      files.push({
        path: file,
        ext,
        quality: q,
        codec: manifest.codecs || null,
        bitDepth: manifest.bitDepth || null,
        sampleRate: manifest.samplingRate || null,
        bitrate: manifest.bandwidth || null,
        label: qualityLabel(q, manifest),
      });
    }
    if (files.length) results.push({ id: t.id, title: t.title, ok: true, files });
    else results.push({ id: t.id, title: t.title, ok: false, error: 'all qualities failed' });
  }
  return results;
}

function gqlHeaders() {
  const h = {
    'user-agent': UA,
    'content-type': 'application/json',
    origin: ORIGIN,
    'x-api-key': FIREFLY_WEB_KEY,
    'music-territory': DEFAULT_TERRITORY,
    'x-amzn-device-id': '13799006441243828',
    'x-amzn-device-type': 'A16ZV8BU3SN1N3',
    'x-amzn-session-id': '137-9900644-1243828',
  };
  if (PREMIUM_COOKIE) h.cookie = PREMIUM_COOKIE;
  return h;
}

async function json(res) {
  const text = await res.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch { data = { raw: text }; }
  return { status: res.status, ok: res.ok, data };
}

async function gql(query, variables = {}) {
  const res = await fetch(GQL, { method: 'POST', headers: gqlHeaders(), body: JSON.stringify({ query, variables }) });
  const { status, ok, data } = await json(res);
  if (!ok) throw new Error(`gql ${status}: ${(data.errors || []).map((e) => e.message).join('; ') || JSON.stringify(data)}`);
  if (data.errors?.length) throw new Error(`gql: ${data.errors.map((e) => e.message).join('; ')}`);
  return data.data;
}

function artwork(images) {
  const arr = images || [];
  const byW = arr.slice().sort((a, b) => (b.width || 0) - (a.width || 0));
  return byW.map((i) => ({ url: i.url, width: i.width || null, height: i.height || null }));
}

let _config = null; let _configAt = 0;
async function getConfig() {
  if (_config && Date.now() - _configAt < 5 * 60 * 1000) return _config;
  const h = { 'user-agent': UA, origin: ORIGIN, referer: `${ORIGIN}/` };
  if (PREMIUM_COOKIE) h.cookie = PREMIUM_COOKIE;
  const res = await fetch(`${CONFIG}?clientApplication=skyfire&skipToken=false`, {
    method: 'POST',
    headers: h,
  });
  const { status, ok, data } = await json(res);
  if (!ok) throw new Error(`config ${status}: ${JSON.stringify(data)}`);
  const cookies = {};
  for (const line of (res.headers.getSetCookie?.() || [])) {
    const m = line.match(/^([^=;]+)=([^;]*)/);
    if (m) cookies[m[1]] = m[2];
  }
  _config = { config: data, cookies };
  _configAt = Date.now();
  return _config;
}

function cookieHeader(cookies) {
  return Object.entries(cookies).map(([k, v]) => `${k}=${v}`).join('; ');
}

function searchBody(query, { deviceId, deviceType }, { limit = 10 } = {}) {
  const cr = { allowedParentalControls: { hasExplicitLanguage: true }, assetQuality: { quality: [] }, contentTier: null };
  const spec = (type, fields) => ({ documentSpecs: [{ type, fields }], maxResults: limit, contentRestrictions: cr });
  return {
    customerIdentity: { deviceId, deviceType },
    features: { spellCorrection: { allowCorrection: true }, upsell: { allowUpsellForCatalogContent: true } },
    locale: DEFAULT_LOCALE,
    musicTerritory: DEFAULT_TERRITORY,
    query,
    resultSpecs: [
      { ...spec('catalog_track', ['asin', 'title', 'artistName']), label: 'Tracks' },
      { ...spec('catalog_album', ['asin', 'title', 'artistName']), label: 'Albums' },
      { ...spec('catalog_artist', ['asin', 'name']), label: 'Artists' },
    ],
  };
}

function tenzingHeaders({ config, cookies }) {
  return {
    'user-agent': UA,
    'Content-Encoding': 'amz-1.0',
    'Content-Type': 'application/json; charset=UTF-8',
    Accept: 'application/json',
    'X-Amz-Target': 'com.amazon.tenzing.textsearch.v1_1.TenzingTextSearchServiceExternalV1_1.search',
    'x-amz-access-token': config.accessToken || '',
    'x-amz-device-id': config.deviceId,
    'x-amz-device-type': config.deviceType,
    'x-amzn-session-id': config.sessionId,
    'csrf-token': config.csrf.token,
    'csrf-ts': String(config.csrf.ts),
    'csrf-rnd': String(config.csrf.rnd),
    cookie: cookieHeader(cookies),
  };
}

async function search(query, { limit = 10 } = {}) {
  const { config, cookies } = await getConfig();
  const res = await fetch(TENZING, {
    method: 'POST',
    headers: tenzingHeaders({ config, cookies }),
    body: JSON.stringify(searchBody(query, config, { limit })),
  });
  const { status, ok, data } = await json(res);
  if (!ok) throw new Error(`search ${status}: ${JSON.stringify(data)}`);
  const out = {};
  for (const block of data.results || []) {
    const label = block.label || block.blockRef?.split('|')[1];
    const hits = (block.hits || []).map((h) => {
      const d = h.document || {};
      return {
        asin: d.asin,
        title: d.title || d.name,
        artistName: d.artistName || null,
        primeStatus: d.primeStatus || null,
        isMusicSubscription: d.isMusicSubscription || null,
        type: (d.__type || '').replace('com.amazon.music.platform.model#', ''),
      };
    });
    if (label === 'Tracks') out.tracks = hits;
    else if (label === 'Albums') out.albums = hits;
    else if (label === 'Artists') out.artists = hits;
  }
  return out;
}

async function getTrack(id) {
  const t = await gql(`query($id: String!) { track(id: $id) { id title duration isrc images { url width height } album { id title } contributingArtists { edges { node { id name } } } } }`, { id });
  const track = t.track;
  if (!track) throw new Error(`track ${id} not found`);
  return {
    type: 'track',
    id: track.id,
    title: track.title,
    durationSeconds: track.duration,
    album: track.album,
    artists: (track.contributingArtists?.edges || []).map((e) => ({ id: e.node.id, name: e.node.name })),
    artwork: artwork(track.images),
    previewUrl: `${SAMPLE}/${track.id}`,
  };
}

async function getAlbum(id) {
  const a = await gql(`query($id: String!) { album(id: $id) { id title copyright releaseDate trackCount duration images { url width height } contributingArtists { edges { node { id name } } } tracks { id title trackNumber duration contributingArtists { edges { node { id name } } } } } }`, { id });
  const album = a.album;
  if (!album) throw new Error(`album ${id} not found`);
  return {
    type: 'album',
    id: album.id,
    title: album.title,
    copyright: album.copyright,
    releaseDate: album.releaseDate,
    trackCount: album.trackCount,
    durationSeconds: album.duration,
    artists: (album.contributingArtists?.edges || []).map((e) => ({ id: e.node.id, name: e.node.name })),
    artwork: artwork(album.images),
    tracks: (album.tracks || []).map((t) => ({
      id: t.id,
      title: t.title,
      isrc: t.isrc,
      trackNumber: t.trackNumber,
      durationSeconds: t.duration,
      artists: (t.contributingArtists?.edges || []).map((e) => ({ id: e.node.id, name: e.node.name })),
      previewUrl: `${SAMPLE}/${t.id}`,
    })),
  };
}

async function getArtist(id, { albumLimit = 50 } = {}) {
  const a = await gql(`query($id: String!, $n: Float!) { artist(id: $id) { id name images { url width height } albums(limit: $n) { edges { node { id title trackCount releaseDate } } } } }`, { id, n: albumLimit });
  const artist = a.artist;
  if (!artist) err(`artist ${id} not found`);
  return {
    type: 'artist',
    id: artist.id,
    name: artist.name,
    artwork: artwork(artist.images),
    albums: (artist.albums?.edges || []).map((e) => ({ id: e.node.id, title: e.node.title, trackCount: e.node.trackCount, releaseDate: e.node.releaseDate })),
  };
}

async function getPlaylist(id, { trackLimit = 1000 } = {}) {
  const p = await gql(`query($id: String!, $n: Float!) { playlist(id: $id) { id title trackCount description images { url width height } tracks(limit: $n) { edges { node { id title duration contributingArtists { edges { node { id name } } } } } } } }`, { id, n: trackLimit });
  const playlist = p.playlist;
  if (!playlist) err(`playlist ${id} not found`);
  return {
    type: 'playlist',
    id: playlist.id,
    title: playlist.title,
    description: playlist.description || null,
    trackCount: playlist.trackCount,
    artwork: artwork(playlist.images),
    tracks: (playlist.tracks?.edges || []).map((e) => ({
      id: e.node.id,
      title: e.node.title,
      isrc: e.node.isrc,
      durationSeconds: e.node.duration,
      artists: (e.node.contributingArtists?.edges || []).map((x) => ({ id: x.node.id, name: x.node.name })),
      previewUrl: `${SAMPLE}/${e.node.id}`,
    })),
  };
}

async function downloadPreview(id, outPath) {
  const out = outPath || `${id}.preview.mp3`;
  const res = await fetch(`${SAMPLE}/${id}`, { headers: { 'user-agent': UA, origin: ORIGIN }, redirect: 'follow' });
  if (!res.ok) throw new Error(`preview ${res.status} ${res.statusText}`);
  const bytes = Buffer.from(await res.arrayBuffer());
  if (out === '-') { process.stdout.write(bytes); return bytes.length; }
  writeFileSync(out, bytes);
  process.stderr.write(`${out}: ${(bytes.length / 1048576).toFixed(1)} MiB\n`);
  return bytes.length;
}

function parseMpd(mpd) {
  const pssh = (mpd.match(/<cenc:pssh>([^<]+)<\/cenc:pssh>/) || [])[1] || null;
  const kid = (mpd.match(/cenc:default_KID="([^"]+)"/) || [])[1] || null;
  const licenseUrl = (mpd.match(/<amz:LicenseUrl>([^<]+)<\/amz:LicenseUrl>/) || [])[1] || null;
  const duration = (mpd.match(/mediaPresentationDuration="PT([^"]+)"/) || [])[1] || null;
  const baseUrl = ((mpd.match(/<BaseURL>([^<]+)<\/BaseURL>/) || [])[1] || null)?.replace(/&amp;/g, '&') || null;
  const codecs = (mpd.match(/codecs="([^"]+)"/) || [])[1] || null;
  const samplingRate = (mpd.match(/audioSamplingRate="([0-9]+)"/) || [])[1] || null;
  const bandwidth = (mpd.match(/bandwidth="([0-9]+)"/) || [])[1] || null;
  const bitDepth = (mpd.match(/amz-music:bitDepth" value="([^"]+)"/) || [])[1] || null;
  const streamName = (mpd.match(/tag:amazon\.com,2019:dash:StreamName" value="([^"]+)"/) || [])[1] || null;
  return {
    pssh,
    kid,
    kidHex: kid ? kid.replace(/-/g, '') : null,
    licenseUrl,
    duration,
    baseUrl,
    codecs,
    samplingRate: samplingRate ? Number(samplingRate) : null,
    bandwidth: bandwidth ? Number(bandwidth) : null,
    bitDepth: bitDepth ? Number(bitDepth) : null,
    streamName,
  };
}

async function getStream(id, { quality = 'hires', saveManifest = '' } = {}) {
  const { config } = await getConfig();
  const cap = QUALITY_CAP[quality] || String(quality).toUpperCase();
  const authToken = Buffer.from(JSON.stringify({ access_token: config.accessToken })).toString('base64');
  const headers = {
    'user-agent': UA,
    'content-type': 'application/json',
    origin: ORIGIN,
    'x-api-key': FIREFLY_AUTH_KEY,
    'music-territory': DEFAULT_TERRITORY,
    'x-amzn-device-id': config.deviceId,
    'x-amzn-device-type': 'A16ZV8BU3SN1N3',
    'x-amzn-session-id': config.sessionId,
    authorization: `AmznMusic ${authToken}`,
  };
  const res = await fetch(GQL, {
    method: 'POST',
    headers,
    body: JSON.stringify({
      query: `query($id: String!) { track(id: $id) { id title duration playbackInformation(drmType: "WIDEVINE", audioCapability: "${cap}") { audioUrl format protocol expireAt licenseHeaders { name value } } } }`,
      variables: { id },
    }),
  });
  const { status, ok, data } = await json(res);
  if (!ok || data.errors?.length) throw new Error(`stream ${status}: ${(data.errors || []).map((e) => e.message).join('; ') || JSON.stringify(data)}`);
  const track = data.data.track;
  if (!track) err(`track ${id} not found`);
  const pi = track.playbackInformation;
  if (!pi?.audioUrl) throw new Error(`no audioUrl for ${id} (premium FireFly auth + cookie required)`);
  const licenseHeaders = Object.fromEntries((pi.licenseHeaders || []).map((h) => [h.name, h.value]));
  const mpd = await (await fetch(pi.audioUrl)).text();
  if (saveManifest) writeFileSync(saveManifest, mpd);
  const manifest = parseMpd(mpd);
  return {
    type: 'track',
    id: track.id,
    title: track.title,
    durationSeconds: track.duration,
    quality,
    audioCapability: cap,
    format: pi.format,
    protocol: pi.protocol,
    expireAt: pi.expireAt,
    audioUrl: pi.audioUrl,
    manifest,
    licenseRequest: {
      url: manifest.licenseUrl,
      method: 'POST',
      headers: {
        ...licenseHeaders,
        'content-type': 'application/json',
        'user-agent': 'Chrome/124.0.0.0 AmazonMusic/1.0',
        'x-amz-music-agent': 'Chrome/124.0.0.0 AmazonMusic/1.0',
        cookie: '<auto-loaded from kuki.json>',
        'csrf-token': config.csrf.token,
        'csrf-ts': String(config.csrf.ts),
        'csrf-rnd': String(config.csrf.rnd),
      },
      body: { licenseChallenge: '<base64 widevine challenge>' },
    },
  };
}

async function probeQualities(id, title) {
  const results = await Promise.all(['hires', 'hifi', 'std'].map((q) => getStream(id, { quality: q }).then((s) => ({ q, s })).catch(() => null)));
  const qualities = [];
  for (const r of results.filter(Boolean)) {
    const { q, s } = r;
    if (s?.audioUrl && s.manifest?.pssh && s.manifest?.licenseUrl && s.manifest?.baseUrl) {
      qualities.push({
        quality: q,
        file: `${sanitize(title)}.${extForCodec(s.manifest.codecs)}`,
        codec: s.manifest.codecs,
        bitDepth: s.manifest.bitDepth,
        sampleRate: s.manifest.samplingRate,
        bitrate: s.manifest.bandwidth,
        streamName: s.manifest.streamName,
        manifestUrl: s.audioUrl,
        mediaUrl: s.manifest.baseUrl,
        licenseUrl: s.manifest.licenseUrl,
        downloadUrl: `/amazon/download/${id}?quality=${q}&title=${encodeURIComponent(sanitize(title))}`,
      });
    }
  }
  return qualities;
}

async function cmdResolve(target, argv) {
  const parsed = /^https?:\/\//i.test(target) ? asinFromUrl(target) : { asin: target, kind: null };
  const { asin, kind } = parsed;
  if (!asin) err('no ASIN found');

  if (kind === 'artists') {
    console.log(JSON.stringify(await getArtist(asin, { albumLimit: Number(flag(argv, '--limit', 50)) }), null, 2));
    return;
  }

  const mk = async (t) => ({
    id: t.id,
    title: t.title,
    trackNumber: t.trackNumber ?? null,
    durationSeconds: t.durationSeconds ?? null,
    artists: t.artists ?? null,
    album: t.album ?? null,
    artwork: t.artwork ?? null,
    previewUrl: t.previewUrl ?? `${SAMPLE}/${t.id}`,
    qualities: await probeQualities(t.id, t.title),
  });

  if (kind === 'playlists') {
    const pl = await getPlaylist(asin, { trackLimit: Number(flag(argv, '--limit', 1000)) });
    const results = [];
    for (const t of (pl.tracks || [])) results.push(await mk(t));
    console.log(JSON.stringify({ type: 'playlist', id: pl.id, title: pl.title, description: pl.description, trackCount: pl.trackCount, artwork: pl.artwork, results }, null, 2));
    return;
  }
  if (kind === 'dp' || kind === 'tracks' || kind === 'track') {
    try {
      const t = await getTrack(asin);
      console.log(JSON.stringify({ type: 'track', ...(await mk(t)) }, null, 2));
      return;
    } catch (_) {}
  }
  if (kind === 'albums') {
    try {
      const album = await getAlbum(asin);
      const results = [];
      for (const t of (album.tracks || [])) results.push(await mk({ ...t, album: { id: album.id, title: album.title }, artwork: album.artwork }));
      console.log(JSON.stringify({ type: 'album', id: album.id, title: album.title, artist: album.artists?.[0]?.name || '', artists: album.artists, artwork: album.artwork, copyright: album.copyright, releaseDate: album.releaseDate, durationSeconds: album.durationSeconds, trackCount: album.trackCount, results }, null, 2));
      return;
    } catch (_) {}
  }

  const track = await getTrack(asin).catch(() => null);
  if (track) {
    console.log(JSON.stringify({ type: 'track', ...(await mk(track)) }, null, 2));
    return;
  }
  const album = await getAlbum(asin).catch(() => null);
  if (album) {
    const results = [];
    for (const t of (album.tracks || [])) results.push(await mk({ ...t, album: { id: album.id, title: album.title }, artwork: album.artwork }));
    console.log(JSON.stringify({ type: 'album', id: album.id, title: album.title, artist: album.artists?.[0]?.name || '', artists: album.artists, artwork: album.artwork, copyright: album.copyright, releaseDate: album.releaseDate, durationSeconds: album.durationSeconds, trackCount: album.trackCount, results }, null, 2));
    return;
  }
  err(`can't resolve ${asin} as track or album`);
}

async function cmdDownload(target, argv) {
  const parsed = /^https?:\/\//i.test(target) ? asinFromUrl(target) : { asin: target, kind: null };
  const { asin, kind } = parsed;
  if (!asin) err('no ASIN found');

  if (kind === 'artists') {
    console.log(JSON.stringify(await getArtist(asin, { albumLimit: Number(flag(argv, '--limit', 50)) }), null, 2));
    return;
  }
  if (kind === 'playlists') {
    const pl = await getPlaylist(asin, { trackLimit: Number(flag(argv, '--limit', 1000)) });
    const results = await downloadTracks((pl.tracks || []).map((t) => ({ id: t.id, title: t.title, artists: t.artists })), argv, { title: pl.title, artwork: pl.artwork });
    console.log(JSON.stringify({ type: 'playlist', id: pl.id, title: pl.title, trackCount: pl.trackCount, downloaded: results.filter((r) => r.ok).length, failed: results.filter((r) => !r.ok).length, results }, null, 2));
    return;
  }
  if (kind === 'dp' || kind === 'tracks' || kind === 'track') {
    const t = await getTrack(asin);
    const results = await downloadTracks([{ id: t.id, title: t.title, isrc: t.isrc, artists: t.artists, album: t.album, artwork: t.artwork }], argv, { title: t.album?.title, artwork: t.artwork });
    console.log(JSON.stringify({ type: 'track', ...results[0] }, null, 2));
    return;
  }
  if (kind === 'albums') {
    const album = await getAlbum(asin);
    const albumMeta = { title: album.title, artists: album.artists, year: (album.releaseDate || '').slice(0, 4) || null, artwork: album.artwork, copyright: album.copyright };
    const results = await downloadTracks((album.tracks || []).map((t) => ({ id: t.id, title: t.title, isrc: t.isrc, trackNumber: t.trackNumber, artists: t.artists })), argv, albumMeta);
    console.log(JSON.stringify({ type: 'album', id: album.id, title: album.title, artist: album.artists?.[0]?.name || '', trackCount: album.trackCount, downloaded: results.filter((r) => r.ok).length, failed: results.filter((r) => !r.ok).length, results }, null, 2));
    return;
  }

  const track = await getTrack(asin).catch(() => null);
  if (track) {
    const results = await downloadTracks([{ id: track.id, title: track.title, isrc: track.isrc, artists: track.artists, album: track.album, artwork: track.artwork }], argv, { title: track.album?.title, artwork: track.artwork });
    console.log(JSON.stringify({ type: 'track', ...results[0] }, null, 2));
    return;
  }
  const album = await getAlbum(asin).catch(() => null);
  if (album) {
    const albumMeta = { title: album.title, artists: album.artists, year: (album.releaseDate || '').slice(0, 4) || null, artwork: album.artwork, copyright: album.copyright };
    const results = await downloadTracks((album.tracks || []).map((t) => ({ id: t.id, title: t.title, isrc: t.isrc, trackNumber: t.trackNumber, artists: t.artists })), argv, albumMeta);
    console.log(JSON.stringify({ type: 'album', id: album.id, title: album.title, artist: album.artists?.[0]?.name || '', trackCount: album.trackCount, downloaded: results.filter((r) => r.ok).length, failed: results.filter((r) => !r.ok).length, results }, null, 2));
    return;
  }
  err(`can't resolve ${asin} as track or album`);
}

async function main() {
  const argv = process.argv.slice(2);
  const cmd = argv[0];
  if (!cmd) err('usage: node amazon.js <url | search|track|album|artist|playlist|preview|stream|resolve|download> <id-or-query> [--limit N] [--quality std|hifi|hires]');

  if (/^https?:\/\//i.test(cmd)) return cmdResolve(cmd, argv);

  if (cmd === 'resolve') {
    const target = String(argv[1]); if (!target) err('usage: node amazon.js resolve <url|asin>');
    return cmdResolve(target, argv);
  }

  if (cmd === 'search') {
    const q = argv[1]; if (!q) err('usage: node amazon.js search <query> [--limit N]');
    const out = await search(q, { limit: Number(flag(argv, '--limit', 10)) });
    console.log(JSON.stringify(out, null, 2));
    return;
  }
  if (cmd === 'track') { console.log(JSON.stringify(await getTrack(String(argv[1])), null, 2)); return; }
  if (cmd === 'album') { console.log(JSON.stringify(await getAlbum(String(argv[1])), null, 2)); return; }
  if (cmd === 'artist') { console.log(JSON.stringify(await getArtist(String(argv[1]), { albumLimit: Number(flag(argv, '--limit', 50)) }), null, 2)); return; }
  if (cmd === 'playlist') { console.log(JSON.stringify(await getPlaylist(String(argv[1]), { trackLimit: Number(flag(argv, '--limit', 1000)) }), null, 2)); return; }
  if (cmd === 'preview') {
    const n = await downloadPreview(String(argv[1]), flag(argv, '--out', ''));
    process.stderr.write(`saved ${n} bytes\n`);
    return;
  }
  if (cmd === 'stream') {
    const id = String(argv[1]); if (!id) err('usage: node amazon.js stream <trackId> [--quality std|hifi|hires] [--save-manifest file.mpd]');
    const out = await getStream(id, { quality: flag(argv, '--quality', 'hires'), saveManifest: flag(argv, '--save-manifest', '') });
    console.log(JSON.stringify(out, null, 2));
    return;
  }
  if (cmd === 'download') {
    const target = String(argv[1]); if (!target) err('usage: node amazon.js download <url|asin> [--device file.wvd] [--outdir dir]');
    return cmdDownload(target, argv);
  }

  err(`unknown command: ${cmd}`);
}

main().catch((e) => { console.error(e.message); process.exitCode = 1; });
