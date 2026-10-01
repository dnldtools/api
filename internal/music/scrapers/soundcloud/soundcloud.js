#!/usr/bin/env node
import { writeFileSync, unlinkSync, renameSync, existsSync } from 'node:fs';
import { execFileSync } from 'node:child_process';

const API = 'https://api-v2.soundcloud.com';
const UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36';
const CLIENT_ID = process.env.SOUNDCLOUD_CLIENT_ID || 'Wq8jpsB4RfUsrezgEFDFfBGhkClF0sUN';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const err = (m) => { throw new Error(m); };
const flag = (a, n, d) => { const i = a.indexOf(n); return i === -1 ? d : a[i + 1]; };

async function jres(res) {
  const text = await res.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch { data = { raw: text }; }
  return { status: res.status, ok: res.ok, data };
}

async function get(path, params = {}) {
  const q = new URLSearchParams({ client_id: CLIENT_ID, ...params });
  const res = await fetch(`${API}/${path}?${q}`, { headers: { 'user-agent': UA, accept: 'application/json' } });
  const { status, ok, data } = await jres(res);
  if (!ok) {
    const e = new Error(`api ${path} ${status}: ${JSON.stringify(data).slice(0, 300)}`);
    e.status = status;
    throw e;
  }
  return data;
}

async function rawFetch(url) {
  const res = await fetch(url, { headers: { 'user-agent': UA } });
  if (!res.ok) throw new Error(`fetch ${res.status} ${res.statusText}: ${url.slice(0, 120)}`);
  return Buffer.from(await res.arrayBuffer());
}

function artwork(u) {
  if (!u) return null;
  const base = u.replace(/-[A-Za-z0-9]+\.(jpe?g|png)(\?.*)?$/, '');
  return {
    '120': `${base}-t120x120.jpg`,
    '300': `${base}-t300x300.jpg`,
    '500': `${base}-t500x500.jpg`,
    original: `${base}-original.jpg`,
    large: u,
  };
}

function pickUser(u) {
  return {
    id: u.id,
    username: u.username,
    permalink: u.permalink,
    permalinkUrl: u.permalink_url,
    fullName: u.full_name || null,
    city: u.city || null,
    country: u.country || null,
    trackCount: u.track_count ?? null,
    playlistCount: u.playlist_count ?? null,
    followersCount: u.followers_count ?? null,
    followingsCount: u.followings_count ?? null,
    avatar: artwork(u.avatar_url),
    description: u.description || null,
    url: u.permalink_url || null,
  };
}

function pickTrack(t) {
  const transcodings = (t.media?.transcodings || []).map((m) => ({
    protocol: m.format.protocol,
    mimeType: m.format.mime_type,
    url: m.url,
  }));
  return {
    id: t.id,
    title: t.title,
    artist: t.user?.username || null,
    artistId: t.user_id ?? t.user?.id ?? null,
    user: t.user ? pickUser(t.user) : null,
    durationMs: t.duration ?? t.full_duration ?? null,
    durationSec: t.duration ? (t.duration / 1000).toFixed(2) : null,
    genre: t.genre || null,
    artwork: artwork(t.artwork_url || t.user?.avatar_url),
    waveformUrl: t.waveform_url || null,
    playbackCount: t.playback_count ?? null,
    likesCount: t.likes_count ?? null,
    repostsCount: t.reposts_count ?? null,
    commentCount: t.comment_count ?? null,
    releaseDate: t.release_date || null,
    createdAt: t.created_at || null,
    downloadable: t.downloadable ?? false,
    streamable: t.streamable ?? false,
    policy: t.policy || null,
    monetizationModel: t.monetization_model || null,
    labelName: t.label_name || null,
    license: t.license || null,
    publisherMetadata: t.publisher_metadata || null,
    permalink: t.permalink || null,
    permalinkUrl: t.permalink_url || null,
    uri: t.uri || null,
    transcodings,
    streamHint: transcodings.some((x) => x.protocol === 'progressive') ? 'progressive-mp3' : transcodings.some((x) => x.protocol === 'hls') ? 'hls' : null,
  };
}

function pickPlaylist(p) {
  return {
    id: p.id,
    title: p.title,
    user: p.user ? pickUser(p.user) : null,
    description: p.description || null,
    genre: p.genre || null,
    trackCount: p.track_count ?? (p.tracks?.length || 0),
    durationMs: p.duration ?? null,
    likesCount: p.likes_count ?? null,
    repostsCount: p.reposts_count ?? null,
    artwork: artwork(p.artwork_url || p.user?.avatar_url),
    tags: p.tag_list || null,
    createdAt: p.created_at || null,
    permalink: p.permalink || null,
    permalinkUrl: p.permalink_url || null,
    tracks: (p.tracks || []).map(pickTrack),
  };
}

function cleanUrl(u) {
  const s = String(u).trim();
  if (!/^https?:\/\//i.test(s)) err(`not a url: ${u}`);
  const base = s.split('?')[0].split('#')[0];
  return base;
}

async function resolveUrl(u) {
  const url = cleanUrl(u);
  const d = await get('resolve', { url });
  if (d.kind === 'track') return { type: 'track', ...pickTrack(d) };
  if (d.kind === 'playlist') return { type: 'playlist', ...pickPlaylist(d) };
  if (d.kind === 'user') return { type: 'user', ...pickUser(d) };
  return { type: d.kind || 'unknown', raw: d };
}

async function getTrack(id) {
  return pickTrack(await get(`tracks/${id}`));
}

async function getPlaylist(id) {
  return pickPlaylist(await get(`playlists/${id}`));
}

async function getUser(id) {
  return pickUser(await get(`users/${id}`));
}

async function streamFormats(track) {
  const trans = track.transcodings || [];
  const byCodec = new Map();
  for (const c of trans) {
    const codec = codecOf(c.mimeType);
    if (!byCodec.has(codec)) byCodec.set(codec, []);
    byCodec.get(codec).push(c);
  }
  const out = [];
  for (const [codec, list] of byCodec) {
    const pick = list.find((x) => x.protocol === 'progressive') || list.find((x) => x.protocol === 'hls') || list.find((x) => !/encrypted/i.test(x.protocol)) || list[0];
    try {
      const res = await fetch(`${pick.url}?client_id=${CLIENT_ID}`, { headers: { 'user-agent': UA } });
      const { ok, data } = await jres(res);
      if (!ok || !data.url) continue;
      out.push({ codec, protocol: pick.protocol, url: data.url, ext: extOfCodec(codec), mimeType: pick.mimeType });
    } catch {}
  }
  return out;
}

function codecOf(mimeType) {
  const m = String(mimeType || '').toLowerCase();
  if (m.includes('mpeg') || m.includes('mp3')) return 'mp3';
  if (m.includes('opus') || m.includes('webm')) return 'opus';
  if (m.includes('mp4a') || m.includes('aac') || m.includes('mp4')) return 'aac';
  return 'mp3';
}

function extOfCodec(codec) {
  if (codec === 'aac') return 'm4a';
  if (codec === 'opus') return 'opus';
  return 'mp3';
}

async function probeBitrate(path) {
  try {
    const out = execFileSync('ffprobe', ['-v', 'error', '-show_entries', 'stream=bit_rate', '-of', 'default=nw=1:nk=1', path], { encoding: 'utf8' });
    const n = Number((out.split('\n')[0] || '').trim());
    return Number.isFinite(n) && n > 0 ? Math.round(n / 1000) : 0;
  } catch { return 0; }
}

function formatLabel(codec, protocol, bitrate) {
  const kind = codec === 'aac' ? 'AAC' : codec.toUpperCase();
  const proto = protocol === 'progressive' ? 'progressive' : 'HLS';
  return bitrate ? `${kind} · ${bitrate} kbps (${proto})` : `${kind} (${proto})`;
}

function runFfmpeg(args) {
  try { execFileSync('ffmpeg', args, { stdio: 'inherit' }); return true; }
  catch { return false; }
}

async function tagFile(path, track) {
  const meta = track.publisherMetadata || {};
  const coverUrl = track.artwork?.['500'] || track.artwork?.original || track.artwork?.large;
  let cover = null;
  if (coverUrl) {
    try { cover = `${path}.cover.jpg`; writeFileSync(cover, await rawFetch(coverUrl)); } catch { cover = null; }
  }
  const args = ['-y', '-hide_banner', '-loglevel', 'error', '-i', path];
  if (cover) args.push('-i', cover);
  args.push('-map', '0:a');
  if (cover) args.push('-map', '1:v', '-c:v', 'copy', '-disposition:v:0', 'attached_pic');
  args.push('-c:a', 'copy');
  const set = (k, v) => { if (v != null && v !== '') args.push('-metadata', `${k}=${v}`); };
  set('title', track.title);
  set('artist', track.user?.username || track.artist);
  set('album', meta.album_title);
  set('date', track.releaseDate ? track.releaseDate.slice(0, 4) : null);
  set('genre', track.genre);
  set('isrc', meta.isrc);
  set('label', track.labelName);
  set('comment', `soundcloud ${track.id}`);
  const dot = path.lastIndexOf('.');
  const tmp = dot === -1 ? `${path}.tagging` : `${path.slice(0, dot)}.tagging${path.slice(dot)}`;
  const ok = runFfmpeg([...args, tmp]);
  if (cover) { try { unlinkSync(cover); } catch {} }
  if (ok) { try { unlinkSync(path); renameSync(tmp, path); } catch { try { unlinkSync(tmp); } catch {} } }
  else { try { unlinkSync(tmp); } catch {} }
  return path;
}

async function downloadTrackFormats(track, outPrefix) {
  const forms = await streamFormats(track);
  if (!forms.length) err(`no streamable transcoding for track ${track.id}`);
  const files = [];
  for (const s of forms) {
    const out = `${outPrefix}.${s.ext}`;
    try {
      if (s.protocol === 'progressive') {
        writeFileSync(out, await rawFetch(s.url));
      } else {
        if (!runFfmpeg(['-y', '-hide_banner', '-loglevel', 'error', '-i', s.url, '-c', 'copy', out])) throw new Error('ffmpeg rejected input');
      }
      if (!existsSync(out)) throw new Error('no output produced');
      await tagFile(out, track);
      const bitrate = await probeBitrate(out);
      files.push({
        path: out,
        ext: s.ext,
        codec: s.codec,
        quality: s.protocol === 'progressive' ? 'PROGRESSIVE' : 'HLS',
        bitrate,
        label: formatLabel(s.codec, s.protocol, bitrate),
      });
    } catch (e) { process.stderr.write(`skip ${s.codec}: ${e.message}\n`); }
  }
  if (!files.length) err(`every transcoding failed for track ${track.id}`);
  return files;
}

async function search(q, type = 'tracks', limit = 10) {
  const d = await get(`search/${type}`, { q, limit });
  const c = d.collection || [];
  if (type === 'tracks') return c.map(pickTrack);
  if (type === 'playlists') return c.map(pickPlaylist);
  if (type === 'users') return c.map(pickUser);
  return c;
}

async function cmdUrl(u) {
  const r = await resolveUrl(u);
  if (r.type === 'track') {
    console.log(JSON.stringify(r, null, 2));
    return;
  }
  if (r.type === 'playlist') {
    console.log(JSON.stringify(r, null, 2));
    return;
  }
  if (r.type === 'user') {
    console.log(JSON.stringify(r, null, 2));
    return;
  }
  console.log(JSON.stringify(r, null, 2));
}

async function cmdDownload(arg, argv) {
  const out = flag(argv, '--out', '');
  let track;
  if (/^\d+$/.test(String(arg))) track = await getTrack(Number(arg));
  else {
    const r = await resolveUrl(arg);
    if (r.type === 'playlist') {
      const list = r.tracks || [];
      const dir = out || `sc-playlist-${r.id}`;
      const fs = await import('node:fs');
      fs.mkdirSync(dir, { recursive: true });
      for (let i = 0; i < list.length; i++) {
        process.stderr.write(`[${i + 1}/${list.length}] ${list[i].title}\n`);
        try { await downloadTrackFormats(list[i], `${dir}/${String(i + 1).padStart(2, '0')} - ${list[i].title.replace(/[\\/:*?"<>|]/g, '_')}`); }
        catch (e) { process.stderr.write(`  skip: ${e.message}\n`); }
      }
      process.stderr.write(`done -> ${dir}\n`);
      return;
    }
    if (r.type === 'track') track = r;
    else err(`cannot download a ${r.type}`);
  }
  const files = await downloadTrackFormats(track, out);
  console.log(JSON.stringify({ ok: true, id: String(track.id), files }));
}

async function main() {
  const argv = process.argv.slice(2);
  const cmd = argv[0];
  if (!cmd) err('usage: node soundcloud.js <url | search | download | track | playlist | user> ...');

  if (/^https?:\/\//i.test(cmd)) return cmdUrl(cmd);

  if (cmd === 'search') {
    const q = argv[1]; if (!q) err('usage: node soundcloud.js search <query> [--type tracks|users|playlists] [--limit N]');
    const out = await search(q, flag(argv, '--type', 'tracks'), Number(flag(argv, '--limit', 10)));
    console.log(JSON.stringify(out, null, 2));
    return;
  }

  if (cmd === 'download') {
    if (!argv[1]) err('usage: node soundcloud.js download <track-id|url> [--out file.mp3]');
    return cmdDownload(argv[1], argv);
  }

  if (cmd === 'track') { console.log(JSON.stringify(await getTrack(Number(argv[1])), null, 2)); return; }
  if (cmd === 'playlist') { console.log(JSON.stringify(await getPlaylist(String(argv[1])), null, 2)); return; }
  if (cmd === 'user') {
    const id = String(argv[1]);
    const mode = argv[2] || 'profile';
    if (mode === 'tracks') { console.log(JSON.stringify((await get(`users/${id}/tracks`, { limit: 200 })).collection.map(pickTrack), null, 2)); return; }
    if (mode === 'playlists') { console.log(JSON.stringify((await get(`users/${id}/playlists`, { limit: 200 })).collection.map(pickPlaylist), null, 2)); return; }
    if (mode === 'likes') { console.log(JSON.stringify((await get(`users/${id}/likes`, { limit: 200 })).collection.map(pickTrack), null, 2)); return; }
    console.log(JSON.stringify(await getUser(id), null, 2));
    return;
  }

  err(`unknown command: ${cmd}`);
}

main().catch((e) => { console.error(e.message); process.exitCode = 1; });
