#!/usr/bin/env node
import { appendFileSync, createWriteStream, existsSync, mkdirSync, readFileSync, renameSync, unlinkSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const AUTH = 'https://auth.tidal.com/v1/oauth2';
const API = 'https://api.tidal.com/v1';

const CLIENTS = {
  android: { id: 6333, clientId: 'JEdLAXtZAvJYmDgY', clientSecret: 'tLxFsNJhAeA12lINJrpaLxEfWBHLdx4o42n42iMjRcM=' },
  tv: { id: 6337, clientId: 'ANN68eL0HSf4FZ66', clientSecret: 'iXyAtNpW2ed7jKj5wEYT2REtzy0gvmHH4Ykunf7Nbmg=' },
  hifi: { id: 8017, clientId: '6BDSRdpK9hqEBTgU', clientSecret: 'xeuPmY7nbpZ9IIbLAcQ93shka1VNheUAqN6IcszjTG8=' },
  hires: { id: 13108, clientId: 'lw3vR6GE1vtNBsjv', clientSecret: 'Y8tIpqKJxs9BEIwYr0I9bSbMWDsogXJx9LaN3mCHwD4=' },
};

const QUALITIES = ['LOW', 'HIGH', 'LOSSLESS', 'HI_RES', 'HI_RES_LOSSLESS'];
const MIME_EMU = 'application/vnd.tidal.emu';
const MIME_BTS = 'application/vnd.tidal.bts';
const MIME_DASH = 'application/dash+xml';
const MIME_HLS = 'application/vnd.apple.mpegurl';

const TOKEN_FILE = process.env.TIDAL_TOKEN_FILE || fileURLToPath(new URL('./tidal-token.json', import.meta.url));
const DEFAULT_COUNTRY = process.env.TIDAL_COUNTRY || 'US';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const b64decode = (s) => Buffer.from(s, 'base64').toString('utf8');
const form = (obj) => new URLSearchParams(obj).toString();
const err = (m) => { throw new Error(m); };

function client(name) {
  const c = CLIENTS[name];
  if (!c) err(`unknown client: ${name} (android|tv|hifi|hires)`);
  return c;
}

async function json(res) {
  const text = await res.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch { data = { raw: text }; }
  return { status: res.status, ok: res.ok, data };
}

async function oauthTokenPost(body) {
  const res = await fetch(`${AUTH}/token`, {
    method: 'POST',
    headers: { 'content-type': 'application/x-www-form-urlencoded' },
    body: form(body),
  });
  const { status, ok, data } = await json(res);
  if (!ok) throw new Error(`oauth token ${status}: ${JSON.stringify(data)}`);
  return data;
}

async function clientCredentials(name = 'android') {
  const c = client(name);
  return oauthTokenPost({ grant_type: 'client_credentials', client_id: c.clientId, client_secret: c.clientSecret });
}

async function refreshAccessToken(refreshToken, name = 'tv') {
  const c = client(name);
  return oauthTokenPost({ grant_type: 'refresh_token', refresh_token: refreshToken, client_id: c.clientId, client_secret: c.clientSecret });
}

async function deviceAuthorization(scope = 'r_usr w_usr', name = 'tv') {
  const c = client(name);
  const res = await fetch(`${AUTH}/device_authorization`, {
    method: 'POST',
    headers: { 'content-type': 'application/x-www-form-urlencoded' },
    body: form({ client_id: c.clientId, scope }),
  });
  const { status, ok, data } = await json(res);
  if (!ok) throw new Error(`device_authorization ${status}: ${JSON.stringify(data)}`);
  return data;
}

async function pollDeviceToken(deviceCode, scope = 'r_usr w_usr', name = 'tv', timeoutMs = 300000) {
  const c = client(name);
  let interval = 5000;
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const res = await fetch(`${AUTH}/token`, {
      method: 'POST',
      headers: { 'content-type': 'application/x-www-form-urlencoded' },
      body: form({
        client_id: c.clientId,
        client_secret: c.clientSecret,
        device_code: deviceCode,
        grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
        scope,
      }),
    });
    const { status, ok, data } = await json(res);
    if (ok) return data;
    if (data.error === 'authorization_pending') { await sleep(interval); continue; }
    if (data.error === 'slow_down') { interval += 5000; await sleep(interval); continue; }
    if (data.error === 'expired_token') throw new Error('device code expired');
    throw new Error(`device token ${status}: ${JSON.stringify(data)}`);
  }
  throw new Error('device login timed out');
}

function loadTokens() {
  if (existsSync(TOKEN_FILE)) {
    try { return JSON.parse(readFileSync(TOKEN_FILE, 'utf8')); } catch { return null; }
  }
  return null;
}

function saveTokens(tokens) {
  writeFileSync(TOKEN_FILE, JSON.stringify(tokens, null, 2));
}

async function getUserToken(name = 'tv') {
  if (process.env.TIDAL_TOKEN) return process.env.TIDAL_TOKEN;
  const t = loadTokens();
  if (!t || !t.accessToken) err('no saved session — run: node tidal.js login');
  if (Date.now() < (t.expiresAt || 0)) return t.accessToken;
  if (!t.refreshToken) err('token expired and no refresh token — run: node tidal.js login');
  const fresh = await refreshAccessToken(t.refreshToken, name);
  t.accessToken = fresh.access_token;
  t.refreshToken = fresh.refresh_token || t.refreshToken;
  t.expiresAt = Date.now() + (fresh.expires_in || 14400) * 1000 - 60000;
  saveTokens(t);
  return t.accessToken;
}

async function api(path, { token, query = {} } = {}) {
  const q = new URLSearchParams({ countryCode: DEFAULT_COUNTRY, ...query });
  const headers = {};
  if (token) headers.authorization = `Bearer ${token}`;
  const res = await fetch(`${API}/${path}?${q}`, { headers });
  const { status, ok, data } = await json(res);
  if (!ok) {
    const e = new Error(`api ${path} ${status}: ${data.userMessage || JSON.stringify(data)}`);
    e.status = status;
    e.subStatus = data.subStatus;
    throw e;
  }
  return data;
}

async function anonApi(path, query) {
  const t = await clientCredentials('android');
  return api(path, { token: t.access_token, query });
}

function coverImages(cover) {
  if (!cover) return null;
  const base = `https://resources.tidal.com/images/${cover.replace(/-/g, '/')}`;
  return {
    '80': `${base}/80x80.jpg`,
    '160': `${base}/160x160.jpg`,
    '320': `${base}/320x320.jpg`,
    '640': `${base}/640x640.jpg`,
    '750': `${base}/750x750.jpg`,
    '1280': `${base}/1280x1280.jpg`,
  };
}

function pickTrack(item) {
  return {
    id: item.id,
    title: item.title,
    version: item.version || null,
    duration: item.duration,
    isrc: item.isrc || null,
    audioQuality: item.audioQuality,
    explicit: item.explicit,
    artist: item.artist?.name,
    artists: (item.artists || []).map((a) => a.name),
    album: item.album?.title,
    albumId: item.album?.id,
    cover: coverImages(item.album?.cover),
    trackNumber: item.trackNumber,
    discNumber: item.volumeNumber ?? item.discNumber ?? 1,
    copyright: item.copyright || null,
    allowStreaming: item.allowStreaming,
    premiumStreamingOnly: item.premiumStreamingOnly,
    audioModes: item.audioModes || null,
    popularity: item.popularity ?? null,
    url: item.url ? `https://tidal.com/track/${item.id}` : null,
  };
}

const search = (query, { limit = 10, type } = {}) => anonApi('search', { query, limit, ...(type ? { type } : {}) });
const getTrack = (id) => anonApi(`tracks/${id}`).then(pickTrack);
const getAlbum = (id) => anonApi(`albums/${id}`);
const getAlbumTracks = (id) => anonApi(`albums/${id}/tracks`, { limit: 999 }).then((d) => d.items.map(pickTrack));
const getArtist = (id) => anonApi(`artists/${id}`);
const getArtistAlbums = (id) => anonApi(`artists/${id}/albums`, { limit: 999 }).then((d) => d.items);
const getArtistTopTracks = (id) => anonApi(`artists/${id}/toptracks`, { limit: 999 }).then((d) => d.items.map(pickTrack));
const getPlaylist = (id) => anonApi(`playlists/${id}`);
async function getPlaylistItems(id) {
  const items = [];
  let offset = 0;
  for (;;) {
    const d = await anonApi(`playlists/${id}/items`, { limit: 100, offset });
    items.push(...(d.items || []));
    if (!d.items?.length || items.length >= (d.totalNumberOfItems || 0)) break;
    offset += 100;
  }
  return items.map((i) => ({ type: i.type, item: i.item }));
}

function parseTidalUrl(u) {
  const m = String(u).match(/\/(?:browse\/)?(track|album|artist|playlist|video|mix)\/([\dA-Fa-f-]+)/i);
  if (!m) err(`cannot parse tidal url: ${u}`);
  return { type: m[1].toLowerCase(), id: m[2] };
}

async function tryStream(trackId, quality) {
  try {
    const pbi = await getPlaybackInfo(trackId, { quality });
    return { stream: streamInfo(trackId, pbi) };
  } catch (e) {
    return { stream: null, error: e.message };
  }
}

async function cmdUrl(url, argv) {
  const { type, id } = parseTidalUrl(url);
  const quality = flag(argv, '--quality', 'LOSSLESS');

  if (type === 'track') {
    const meta = await getTrack(id);
    const s = await tryStream(id, quality);
    console.log(JSON.stringify({ type: 'track', ...meta, ...s }, null, 2));
    return;
  }

  if (type === 'album') {
    const album = await getAlbum(id);
    const tracks = await getAlbumTracks(id);
    if (argv.includes('--download')) {
      const artistName = album.artist?.name || album.artists?.[0]?.name || 'Unknown Artist';
      const dir = flag(argv, '--out', `${artistName} - ${album.title}`.replace(/[\\/:*?"<>|]/g, '_'));
      mkdirSync(dir, { recursive: true });
      const year = (album.releaseDate || '').slice(0, 4) || null;
      const results = [];
      for (const t of tracks) {
        const base = `${String(t.trackNumber ?? t.id).padStart(2, '0')} - ${t.artist} - ${t.title}`.replace(/[\\/:*?"<>|]/g, '_');
        try {
          const out = await downloadTrack(String(t.id), join(dir, base), { quality, meta: t, year });
          results.push({ id: t.id, title: t.title, ok: true, out });
        } catch (e) { results.push({ id: t.id, title: t.title, ok: false, error: e.message }); }
      }
      console.log(JSON.stringify({ type: 'album', id: album.id, title: album.title, artist: artistName, year, downloaded: results.filter((r) => r.ok).length, failed: results.filter((r) => !r.ok).length, results }, null, 2));
      return;
    }
    const items = [];
    for (const t of tracks) {
      const s = await tryStream(t.id, quality);
      items.push({ ...t, ...s });
    }
    console.log(JSON.stringify({
      type: 'album',
      id: album.id,
      title: album.title,
      artist: album.artist?.name || album.artists?.[0]?.name,
      year: album.releaseDate,
      trackCount: album.numberOfTracks,
      cover: coverImages(album.cover),
      tracks: items,
    }, null, 2));
    return;
  }

  if (type === 'artist') {
    const artist = await getArtist(id);
    const albums = await getArtistAlbums(id);
    console.log(JSON.stringify({ type: 'artist', id: artist.id, name: artist.name, albums }, null, 2));
    return;
  }

  if (type === 'playlist') {
    const pl = await getPlaylist(id);
    const rows = await getPlaylistItems(id);
    if (argv.includes('--download')) {
      const dir = flag(argv, '--out', pl.title || id);
      mkdirSync(dir, { recursive: true });
      const results = [];
      for (const r of rows) {
        if (r.type !== 'track' || !r.item?.id) continue;
        const track = pickTrack(r.item);
        try {
          const base = `${String(track.trackNumber || track.id).padStart(2, '0')} - ${track.artist} - ${track.title}`.replace(/[\\/:*?"<>|]/g, '_');
          const out = await downloadTrack(String(track.id), join(dir, base), { quality, meta: track });
          results.push({ id: track.id, title: track.title, ok: true, out });
        } catch (e) { results.push({ id: track.id, title: track.title, ok: false, error: e.message }); }
      }
      console.log(JSON.stringify({ type: 'playlist', id: pl.uuid || pl.id, title: pl.title, downloaded: results.filter((r) => r.ok).length, failed: results.filter((r) => !r.ok).length, results }, null, 2));
      return;
    }
    const items = rows.map((r) => ({ id: r.item?.id, title: r.item?.title, artist: r.item?.artist?.name || r.item?.artists?.[0]?.name, album: r.item?.album?.title }));
    console.log(JSON.stringify({ type: 'playlist', id: pl.uuid || pl.id, title: pl.title, trackCount: pl.numberOfTracks, items }, null, 2));
    return;
  }

  if (type === 'video') {
    const v = await anonApi(`videos/${id}`);
    console.log(JSON.stringify({ type: 'video', ...v }, null, 2));
    return;
  }

  err(`unsupported type: ${type}`);
}

async function getPlaybackInfo(trackId, { quality = 'HIGH', presentation = 'FULL', mode = 'STREAM', immersive = false } = {}) {
  if (!QUALITIES.includes(quality)) err(`quality must be one of ${QUALITIES.join('|')}`);
  const token = await getUserToken();
  return api(`tracks/${trackId}/playbackinfopostpaywall`, {
    token,
    query: {
      playbackmode: mode,
      assetpresentation: presentation,
      audioquality: quality,
      immersiveaudio: immersive ? 'true' : 'false',
      streamingsessionid: randomUUID(),
    },
  });
}

function parseDash(xml) {
  const clean = xml.replace(/&amp;/g, '&');
  const rep = clean.match(/<Representation\b[^>]*>/) || [];
  const repAttrs = rep[0] || '';
  const codecs = (repAttrs.match(/codecs="([^"]+)"/) || [])[1] || null;
  const bandwidth = (repAttrs.match(/bandwidth="([^"]+)"/) || [])[1] || null;
  const st = clean.match(/<SegmentTemplate\b[^>]*>/) || [];
  const stAttrs = st[0] || '';
  const init = (stAttrs.match(/initialization="([^"]+)"/) || [])[1] || null;
  const media = (stAttrs.match(/media="([^"]+)"/) || [])[1] || null;
  const startNumber = Number((stAttrs.match(/startNumber="(\d+)"/) || [])[1] || 1);
  const durM = clean.match(/mediaPresentationDuration="([^"]+)"/);
  let duration = null;
  if (durM) {
    const m = durM[1].match(/PT(?:([\d.]+)H)?(?:([\d.]+)M)?(?:([\d.]+)S)?/);
    if (m) duration = (Number(m[1] || 0) * 3600) + (Number(m[2] || 0) * 60) + Number(m[3] || 0);
  }
  const timeline = [...clean.matchAll(/<S\b([^>]*)\/?>/g)].map((x) => ({
    d: Number((x[1].match(/ d="(\d+)"/) || x[1].match(/d="(\d+)"/) || [])[1] || 0),
    r: Number((x[1].match(/ r="(\d+)"/) || x[1].match(/r="(\d+)"/) || [])[1] || 0),
  }));
  let segmentCount = 0;
  for (const s of timeline) segmentCount += 1 + s.r;
  const segments = [];
  if (media) {
    let n = startNumber;
    for (const s of timeline) {
      for (let i = 0; i <= s.r; i++) segments.push(media.replace(/\$Number\$/, String(n++)));
    }
  }
  return { codecs, bandwidth, init, media, startNumber, duration, segmentCount, segments };
}

function decodeManifest(pbi) {
  const mime = pbi.manifestMimeType;
  const raw = pbi.manifest || '';
  let decoded = raw;
  try { decoded = b64decode(raw); } catch {}
  if (mime === MIME_EMU || mime === MIME_BTS) {
    try {
      const j = JSON.parse(decoded);
      return { mime, urls: j.urls || [], codecs: j.codecs || null };
    } catch { return { mime, urls: [], codecs: null }; }
  }
  if (mime === MIME_DASH) {
    if (/^https?:\/\//.test(decoded.trim())) return { mime, urls: [decoded.trim()], codecs: null };
    const dash = parseDash(decoded);
    return { mime, urls: dash.init ? [dash.init, ...dash.segments] : [], codecs: dash.codecs, dash };
  }
  if (mime === MIME_HLS) {
    const lines = decoded.split('\n').filter((l) => l && !l.startsWith('#'));
    return { mime, urls: lines, codecs: null, playlist: decoded };
  }
  return { mime, urls: [], codecs: null };
}

function streamInfo(trackId, pbi) {
  const m = decodeManifest(pbi);
  const s = {
    trackId,
    audioQuality: pbi.audioQuality,
    audioMode: pbi.audioMode || 'STEREO',
    bitDepth: pbi.bitDepth,
    sampleRate: pbi.sampleRate,
    assetPresentation: pbi.assetPresentation,
    manifestMimeType: m.mime,
    codecs: m.codecs,
    urls: m.urls,
    licenseUrl: pbi.licenseUrl || null,
    licenseSecurityToken: pbi.licenseSecurityToken || null,
    manifestHash: pbi.manifestHash || null,
    streamingSessionId: pbi.streamingSessionId || null,
  };
  if (m.dash) {
    s.duration = m.dash.duration;
    s.initUrl = m.dash.init;
    s.mediaTemplate = m.dash.media;
    s.segmentCount = m.dash.segmentCount;
    s.segments = m.dash.segments;
  } else if (m.playlist) {
    s.playlist = m.playlist;
  }
  return s;
}

async function downloadUrl(url, outPath, token) {
  const res = await fetch(url, { headers: token ? { authorization: `Bearer ${token}` } : {} });
  if (!res.ok) throw new Error(`download ${res.status} ${res.statusText}`);
  const total = Number(res.headers.get('content-length')) || 0;
  let received = 0;
  const start = Date.now();
  const bytes = new Uint8Array(await res.arrayBuffer());
  if (outPath === '-') { process.stdout.write(Buffer.from(bytes)); return bytes.length; }
  writeFileSync(outPath, Buffer.from(bytes));
  received = bytes.length;
  const secs = (Date.now() - start) / 1000 || 1;
  process.stderr.write(`\r${outPath}: ${(received / 1048576).toFixed(1)} MiB @ ${(received / 1048576 / secs).toFixed(1)} MiB/s        \n`);
  return received;
}

const ffmpegBin = () => process.env.FFMPEG || 'ffmpeg';

function runFfmpeg(args) {
  try {
    execFileSync(ffmpegBin(), args, { stdio: ['ignore', 'ignore', 'pipe'] });
  } catch (e) {
    const tail = String(e.stderr || '').split('\n').filter(Boolean).slice(-4).join(' | ');
    throw new Error(`ffmpeg: ${tail || e.message}`);
  }
}

function tagArgs(meta) {
  const set = (k, v) => (v != null && v !== '' ? ['-metadata', `${k}=${String(v)}`] : []);
  return [
    ...set('title', meta.version ? `${meta.title} (${meta.version})` : meta.title),
    ...set('artist', meta.artist),
    ...set('album_artist', meta.artist),
    ...set('album', meta.album),
    ...set('date', meta.year),
    ...set('track', meta.trackNumber),
    ...set('disc', meta.discNumber && meta.discNumber > 1 ? meta.discNumber : null),
    ...set('isrc', meta.isrc),
    ...set('copyright', meta.copyright),
    ...set('comment', `tidal ${meta.id}`),
  ];
}

async function fetchCoverTemp(url) {
  if (!url) return null;
  try {
    const res = await fetch(url);
    if (!res.ok) return null;
    const tmp = join(tmpdir(), `tidal-cover-${randomUUID()}.jpg`);
    writeFileSync(tmp, Buffer.from(await res.arrayBuffer()));
    return tmp;
  } catch { return null; }
}

async function embedMetadata(inPath, meta) {
  const ext = inPath.includes('.') ? inPath.slice(inPath.lastIndexOf('.')) : '.m4a';
  const tmp = `${inPath}.tagging${ext}`;
  const cover = await fetchCoverTemp(meta.cover?.['1280']);
  const args = ['-y', '-i', inPath];
  if (cover) args.push('-i', cover);
  args.push('-map', '0:a');
  if (cover) args.push('-map', '1:v', '-c:v', 'copy', '-disposition:v:0', 'attached_pic');
  args.push('-c:a', 'copy');
  if (/\.(m4a|mp4)$/i.test(inPath)) args.push('-movflags', '+faststart');
  args.push('-fflags', '+bitexact', ...tagArgs(meta), tmp);
  runFfmpeg(args);
  if (cover) unlinkSync(cover);
  unlinkSync(inPath);
  renameSync(tmp, inPath);
  if (/\.(m4a|mp4)$/i.test(inPath) && meta.isrc) {
    try { execFileSync('python', [fileURLToPath(new URL('./mp4tags.py', import.meta.url)), inPath, '--isrc', String(meta.isrc)], { stdio: 'ignore' }); }
    catch {}
  }
}

const albumYearCache = new Map();
async function albumYear(albumId) {
  if (albumYearCache.has(albumId)) return albumYearCache.get(albumId);
  let y = null;
  try { const al = await getAlbum(albumId); y = (al.releaseDate || '').slice(0, 4) || null; } catch {}
  albumYearCache.set(albumId, y);
  return y;
}

async function downloadTrack(trackId, outPath, { quality = 'HIGH', meta = null, year = null } = {}) {
  const pbi = await getPlaybackInfo(trackId, { quality });
  const info = streamInfo(trackId, pbi);
  const token = await getUserToken();
  const parts = info.urls?.length ? [...info.urls] : [];
  if (!parts.length) err(`no stream urls — assetPresentation=${info.assetPresentation}; quality ${quality} needs a HiFi/HiFi Plus subscription`);
  let out = outPath || String(trackId);
  if (!/\.(m4a|mp4|flac|m3u8)$/i.test(out)) out = `${out}.${extFor(info)}`;
  if (parts.length === 1) {
    await downloadUrl(parts[0], out, token);
  } else {
    writeFileSync(out, Buffer.alloc(0));
    for (let i = 0; i < parts.length; i++) {
      const res = await fetch(parts[i], { headers: { authorization: `Bearer ${token}` } });
      if (!res.ok) throw new Error(`segment ${i} ${res.status} ${res.statusText}`);
      appendFileSync(out, Buffer.from(await res.arrayBuffer()));
      process.stderr.write(`\rsegment ${i + 1}/${parts.length}`);
    }
    process.stderr.write('\n');
  }
  const canTag = out !== '-' && !info.licenseUrl && info.manifestMimeType !== MIME_HLS;
  if (canTag) {
    try {
      const m = meta || await getTrack(trackId);
      m.year = year ?? null;
      if (m.year == null && m.albumId) m.year = await albumYear(m.albumId);
      await embedMetadata(out, m);
    } catch (e) { process.stderr.write(`tag warning: ${e.message}\n`); }
  }
  console.log(JSON.stringify(info, null, 2));
  return out;
}

function extFor(info) {
  if (info.manifestMimeType === MIME_HLS) return 'm3u8';
  if (String(info.codecs || '').includes('flac')) return 'flac.mp4';
  if (String(info.codecs || '').includes('mp4a')) return 'm4a';
  if (info.audioQuality === 'LOSSLESS' || info.audioQuality.startsWith('HI_RES')) return 'flac';
  return 'm4a';
}

async function cmdLogin(argv) {
  const name = argv.includes('--client') ? argv[argv.indexOf('--client') + 1] : 'tv';
  const scope = argv.includes('--scope') ? argv[argv.indexOf('--scope') + 1] : 'r_usr w_usr';
  const auth = await deviceAuthorization(scope, name);
  console.log(`Open: ${auth.verificationUriComplete || `${auth.verificationUri}/${auth.userCode}`}`);
  console.log(`Code: ${auth.userCode}`);
  console.log('Waiting for approval...');
  const token = await pollDeviceToken(auth.deviceCode, scope, name);
  const out = {
    client: name,
    accessToken: token.access_token,
    refreshToken: token.refresh_token,
    expiresAt: Date.now() + (token.expires_in || 14400) * 1000 - 60000,
    userId: token.userId ?? null,
  };
  saveTokens(out);
  console.log('saved session to tidal-token.json');
}

function flag(argv, name, def) {
  const i = argv.indexOf(name);
  return i === -1 ? def : argv[i + 1];
}

async function main() {
  const argv = process.argv.slice(2);
  const cmd = argv[0];
  if (!cmd) err('usage: node tidal.js <tidal-url | login | search | playback | download> ...');

  if (/^https?:\/\//i.test(cmd)) return cmdUrl(cmd, argv);

  if (cmd === 'login') return cmdLogin(argv);

  if (cmd === 'search') {
    const q = argv[1]; if (!q) err('usage: node tidal.js search <query> [--limit N]');
    const d = await search(q, { limit: Number(flag(argv, '--limit', 10)) });
    const groups = ['topHit', 'artists', 'albums', 'tracks', 'playlists', 'videos'];
    const out = {};
    for (const g of groups) {
      const items = d[g]?.items;
      if (!items?.length) continue;
      out[g] = items.map((i) => ({ id: i.id, title: i.title || i.name, artist: i.artist?.name || i.artists?.[0]?.name, album: i.album?.title }));
    }
    console.log(JSON.stringify(out, null, 2));
    return;
  }

  if (cmd === 'track') { console.log(JSON.stringify(await getTrack(Number(argv[1])), null, 2)); return; }
  if (cmd === 'album') { return cmdUrl(`https://tidal.com/album/${argv[1]}`, argv); }
  if (cmd === 'album-tracks') { console.log(JSON.stringify(await getAlbumTracks(Number(argv[1])), null, 2)); return; }
  if (cmd === 'artist') { console.log(JSON.stringify(await getArtist(Number(argv[1])), null, 2)); return; }
  if (cmd === 'artist-albums') { console.log(JSON.stringify(await getArtistAlbums(Number(argv[1])), null, 2)); return; }
  if (cmd === 'artist-tracks') { console.log(JSON.stringify(await getArtistTopTracks(Number(argv[1])), null, 2)); return; }

  if (cmd === 'playback') {
    const pbi = await getPlaybackInfo(String(argv[1]), { quality: flag(argv, '--quality', 'LOSSLESS') });
    console.log(JSON.stringify(streamInfo(String(argv[1]), pbi), null, 2));
    return;
  }

  if (cmd === 'download') {
    const out = await downloadTrack(String(argv[1]), flag(argv, '--out', ''), { quality: flag(argv, '--quality', 'LOSSLESS') });
    console.log(`saved: ${out}`);
    return;
  }

  err(`unknown command: ${cmd}`);
}

main().catch((e) => { console.error(e.message); process.exitCode = 1; });
