#!/usr/bin/env node
import { writeFileSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const DEVICE_WVD = process.env.APPLE_DEVICE_WVD || join(HERE, 'device.wvd');

const API = 'https://amp-api.music.apple.com';
const ORIGIN = 'https://music.apple.com';
const UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36';
const PREVIEW_UA = 'iTunes/11.1.1 (Windows; Microsoft Windows 7 x64) AppleWebKit/536.30.1';

const DEFAULT_DEV_TOKEN = 'eyJ0eXAiOiJKV1QiLCJhbGciOiJFUzI1NiIsImtpZCI6IldlYlBsYXlLaWQifQ.eyJpc3MiOiJBTVBXZWJQbGF5IiwiaWF0IjoxNzg5Njg4NzA5LCJleHAiOjE3OTU3MzY3MDksInJvb3RfaHR0cHNfb3JpZ2luIjpbImFwcGxlLmNvbSJdfQ.y0gd6YWyrUrZx-YZNZS0xVHkDHGr-kGZ9RrsWRfApGc2-_NNC968VsD36hRU33s5BBs4KdB7LIZTmYqPra097Q';

const DEV_TOKEN = process.env.APPLE_DEV_TOKEN || DEFAULT_DEV_TOKEN;
const MUSIC_USER_TOKEN = process.env.APPLE_MUSIC_USER_TOKEN || '';
const DEFAULT_SF = process.env.APPLE_STOREFRONT || 'us';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const err = (m) => { throw new Error(m); };

function runWv(args) {
  return new Promise((resolve, reject) => {
    const py = spawn('python', [join(HERE, 'apple_wvdecrypt.py'), ...args], { stdio: 'inherit' });
    py.on('error', reject);
    py.on('close', (code) => (code === 0 ? resolve() : reject(new Error(`python exit ${code}`))));
  });
}

function flag(argv, name, def) {
  const i = argv.indexOf(name);
  return i === -1 ? def : argv[i + 1];
}

async function json(res) {
  const text = await res.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch { data = { raw: text }; }
  return { status: res.status, ok: res.ok, data };
}

async function catalog(path, { sf = DEFAULT_SF, query = {} } = {}) {
  const q = new URLSearchParams({ l: 'en-US', ...query });
  const headers = { authorization: `Bearer ${DEV_TOKEN}`, origin: ORIGIN, 'user-agent': UA, accept: 'application/json' };
  if (MUSIC_USER_TOKEN) headers['media-user-token'] = MUSIC_USER_TOKEN;
  const res = await fetch(`${API}/v1/catalog/${sf}/${path}?${q}`, { headers });
  const { status, ok, data } = await json(res);
  if (!ok) {
    const e = new Error(`catalog ${path} ${status}: ${(data.errors || []).map((x) => x.detail || x.title).join('; ') || JSON.stringify(data)}`);
    e.status = status;
    throw e;
  }
  return data;
}

function artwork(url) {
  if (!url || !url.includes('{w}')) return null;
  const mk = (s) => url.replace(/\{w\}/g, String(s)).replace(/\{h\}/g, String(s));
  return { '80': mk(80), '160': mk(160), '320': mk(320), '640': mk(640), '1280': mk(1280) };
}

function albumIdOf(item) {
  const a = item.relationships?.albums?.data?.[0];
  return a ? a.id : null;
}

function artistIdOf(item) {
  const a = item.relationships?.artists?.data?.[0];
  return a ? a.id : null;
}

function pickSong(item) {
  const a = item.attributes || {};
  const previews = (a.previews || []).map((p) => p.url);
  return {
    id: item.id,
    title: a.name,
    artist: a.artistName,
    album: a.albumName,
    albumId: albumIdOf(item),
    artistId: artistIdOf(item),
    durationMs: a.durationInMillis,
    durationSec: a.durationInMillis ? (a.durationInMillis / 1000).toFixed(2) : null,
    isrc: a.isrc || null,
    genreNames: a.genreNames || [],
    audioTraits: a.audioTraits || [],
    releaseDate: a.releaseDate || null,
    trackNumber: a.trackNumber ?? null,
    discNumber: a.discNumber ?? null,
    hasLyrics: a.hasLyrics ?? false,
    explicit: (a.contentRating === 'explicit') || null,
    artwork: artwork(a.artwork?.url),
    previews,
    previewUrl: previews[0] || null,
    assets: a.assets || null,
    url: a.url || null,
  };
}

function pickAlbum(item) {
  const a = item.attributes || {};
  return {
    id: item.id,
    title: a.name,
    artist: a.artistName,
    artistId: artistIdOf(item),
    genreNames: a.genreNames || [],
    releaseDate: a.releaseDate || null,
    trackCount: a.trackCount ?? null,
    isSingle: a.isSingle ?? null,
    isCompilation: a.isCompilation ?? null,
    artwork: artwork(a.artwork?.url),
    url: a.url || null,
  };
}

function pickArtist(item) {
  const a = item.attributes || {};
  return {
    id: item.id,
    name: a.name,
    genreNames: a.genreNames || [],
    artwork: artwork(a.artwork?.url),
    url: a.url || null,
  };
}

const getSong = (id, sf) => catalog(`songs/${id}`, { sf }).then((d) => pickSong(d.data[0]));
const getAlbumMeta = (id, sf) => catalog(`albums/${id}`, { sf }).then((d) => pickAlbum(d.data[0]));
const getAlbumTracks = (id, sf) => catalog(`albums/${id}`, { sf, query: { include: 'tracks' } })
  .then((d) => (d.data[0].relationships?.tracks?.data || []).map(pickSong));
const getArtist = (id, sf) => catalog(`artists/${id}`, { sf }).then((d) => pickArtist(d.data[0]));
const getArtistAlbums = (id, sf) => catalog(`artists/${id}/albums`, { sf, query: { limit: 100 } })
  .then((d) => (d.data || []).map(pickAlbum));
const getArtistSongs = (id, sf) => catalog(`artists/${id}/songs`, { sf, query: { limit: 20 } })
  .then((d) => (d.data || []).map(pickSong));
const getPlaylistMeta = (id, sf) => catalog(`playlists/${id}`, { sf });
const getPlaylistTracks = (id, sf) => catalog(`playlists/${id}`, { sf, query: { include: 'tracks' } })
  .then((d) => (d.data[0].relationships?.tracks?.data || []).map(pickSong));

function parseAppleUrl(u) {
  const s = String(u);
  const i = s.indexOf('?i=');
  let query = null;
  if (i !== -1) { query = s.slice(i + 3).split('&')[0].split('#')[0]; }
  const m = s.match(/music\.apple\.com\/(?:([a-z]{2})\/)?(song|album|artist|playlist|music-video|music-videos)\/(?:[^/]+\/)?([A-Za-z0-9._-]+)/i);
  if (!m) err(`cannot parse apple music url: ${u}`);
  return { type: m[2].toLowerCase(), id: m[3], sf: (m[1] || DEFAULT_SF).toLowerCase(), songId: query };
}

async function search(q, { sf = DEFAULT_SF, types = 'songs,albums,artists,playlists', limit = 10 } = {}) {
  const d = await catalog('search', { sf, query: { term: q, types, limit } });
  const r = d.results || {};
  const out = {};
  if (r.songs?.data) out.songs = r.songs.data.map(pickSong);
  if (r.albums?.data) out.albums = r.albums.data.map(pickAlbum);
  if (r.artists?.data) out.artists = r.artists.data.map(pickArtist);
  if (r.playlists?.data) out.playlists = r.playlists.data.map((p) => ({ id: p.id, name: p.attributes?.name, curator: p.attributes?.curatorName, artwork: artwork(p.attributes?.artwork?.url) }));
  return out;
}

async function cmdUrl(url, argv) {
  const { type, id, sf, songId } = parseAppleUrl(url);

  if (type === 'song') {
    return runWv([id, '--device', DEVICE_WVD, '--sf', sf]);
  }

  if (type === 'album') {
    if (songId) return runWv([songId, '--device', DEVICE_WVD, '--sf', sf]);
    return runWv(['--album', id, '--device', DEVICE_WVD, '--sf', sf]);
  }

  if (type === 'artist') {
    const meta = await getArtist(id, sf);
    const albums = await getArtistAlbums(id, sf);
    const topSongs = await getArtistSongs(id, sf);
    console.log(JSON.stringify({ type: 'artist', ...meta, albums, topSongs }, null, 2));
    return;
  }

  if (type === 'playlist') {
    return runWv(['--playlist', id, '--device', DEVICE_WVD, '--sf', sf]);
  }

  if (type === 'music-video' || type === 'music-videos') {
    const d = await catalog(`music-videos/${id}`, { sf });
    console.log(JSON.stringify({ type: 'music-video', ...d.data[0] }, null, 2));
    return;
  }

  err(`unsupported type: ${type}`);
}

async function downloadPreview(id, outPath, { sf = DEFAULT_SF } = {}) {
  const song = await getSong(id, sf);
  if (!song.previewUrl) err(`no preview available for song ${id}`);
  const out = outPath || `${id}.preview.m4a`;
  const res = await fetch(song.previewUrl, { headers: { 'user-agent': PREVIEW_UA } });
  if (!res.ok) throw new Error(`preview download ${res.status} ${res.statusText}`);
  const bytes = Buffer.from(await res.arrayBuffer());
  if (out === '-') { process.stdout.write(bytes); return song; }
  writeFileSync(out, bytes);
  process.stderr.write(`${out}: ${(bytes.length / 1048576).toFixed(1)} MiB\n`);
  console.log(JSON.stringify({ type: 'track', ...song, savedAs: out }, null, 2));
  return song;
}

async function main() {
  const argv = process.argv.slice(2);
  const cmd = argv[0];
  if (!cmd) err('usage: node apple.js <apple-music-url | search | download | song | album | artist | playlist> ...');

  if (/^https?:\/\//i.test(cmd)) return cmdUrl(cmd, argv);

  if (cmd === 'search') {
    const q = argv[1]; if (!q) err('usage: node apple.js search <query> [--limit N] [--sf us]');
    const out = await search(q, { sf: flag(argv, '--sf', DEFAULT_SF), limit: Number(flag(argv, '--limit', 10)) });
    console.log(JSON.stringify(out, null, 2));
    return;
  }

  if (cmd === 'download') {
    const out = flag(argv, '--out', '');
    await downloadPreview(String(argv[1]), out, { sf: flag(argv, '--sf', DEFAULT_SF) });
    return;
  }

  if (cmd === 'song') { console.log(JSON.stringify(await getSong(String(argv[1]), flag(argv, '--sf', DEFAULT_SF)), null, 2)); return; }
  if (cmd === 'fullstream') {
    if (!MUSIC_USER_TOKEN) err('APPLE_MUSIC_USER_TOKEN kosong (isi cookie media-user-token dari music.apple.com yang udah login)');
    const d = await catalog(`songs/${String(argv[1])}`, { sf: flag(argv, '--sf', DEFAULT_SF), query: { extend: 'assets' } });
    console.log(JSON.stringify(d.data[0]?.attributes?.assets || d.data[0]?.attributes || {}, null, 2));
    return;
  }
  if (cmd === 'webplayback') {
    if (!MUSIC_USER_TOKEN) err('APPLE_MUSIC_USER_TOKEN kosong (isi cookie media-user-token dari music.apple.com yang udah login)');
    const res = await fetch('https://play.music.apple.com/WebObjects/MZPlay.woa/wa/webPlayback', {
      method: 'POST',
      headers: { authorization: `Bearer ${DEV_TOKEN}`, 'media-user-token': MUSIC_USER_TOKEN, origin: ORIGIN, 'content-type': 'application/json', 'user-agent': UA },
      body: JSON.stringify({ salableAdamId: String(argv[1]) }),
    });
    const j = await res.json();
    console.log(JSON.stringify(j.songList?.[0] || j, null, 2));
    return;
  }
  if (cmd === 'album') { console.log(JSON.stringify(await getAlbumMeta(String(argv[1]), flag(argv, '--sf', DEFAULT_SF)), null, 2)); return; }
  if (cmd === 'album-tracks') { console.log(JSON.stringify(await getAlbumTracks(String(argv[1]), flag(argv, '--sf', DEFAULT_SF)), null, 2)); return; }
  if (cmd === 'artist') { console.log(JSON.stringify(await getArtist(String(argv[1]), flag(argv, '--sf', DEFAULT_SF)), null, 2)); return; }
  if (cmd === 'artist-albums') { console.log(JSON.stringify(await getArtistAlbums(String(argv[1]), flag(argv, '--sf', DEFAULT_SF)), null, 2)); return; }
  if (cmd === 'artist-songs') { console.log(JSON.stringify(await getArtistSongs(String(argv[1]), flag(argv, '--sf', DEFAULT_SF)), null, 2)); return; }
  if (cmd === 'playlist') { console.log(JSON.stringify(await getPlaylistTracks(String(argv[1]), flag(argv, '--sf', DEFAULT_SF)), null, 2)); return; }

  err(`unknown command: ${cmd}`);
}

main().catch((e) => { console.error(e.message); process.exitCode = 1; });
