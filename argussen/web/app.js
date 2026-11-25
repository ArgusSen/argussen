'use strict';
/* ArgusSen – app. The screens are drawn here; the Android shell (window.ArgusApp) handles updates, fullscreen and keeping the screen awake. */

const NATIVE = window.ArgusApp || null;
// Default server for the phone app: set by standard.js, which only exists in the Android build (not in the shared code).
const STD_SERVER = window.ARGUS_STD_SERVER || '';
const LABELS = ['Person', 'Car', 'Van', 'Bicycle', 'Motion'];
const DAY_MS = 86400000;

// ---------- storage ----------
function load(k, std) { try { const v = localStorage.getItem('argus.' + k); return v === null ? std : JSON.parse(v); } catch (e) { return std; } }
function save(k, v) { try { localStorage.setItem('argus.' + k, JSON.stringify(v)); } catch (e) { /* ignore */ } }

// ---------- language ----------
// The texts in the code are English and used as keys; the translations are in i18n.js (window.TRANSLATIONS).
// ArgusSen has one language (set by an administrator under Settings → General; "Automatic" there = Home Assistant's
// language). Each device can choose its own; "auto" on the device = ArgusSen's language (remembered as serverLanguage),
// or the phone's/browser's language before the server has answered.
const LANG_NAMES = { auto: 'Automatic', da: 'Dansk', en: 'English', de: 'Deutsch', fr: 'Français', sv: 'Svenska', nb: 'Norsk', nl: 'Nederlands' };
function langCode(v) {
  if (v && v !== 'auto' && LANG_NAMES[v]) return v;
  const sv = load('serverLanguage', '');
  if (sv && sv !== 'auto' && LANG_NAMES[sv]) return sv;
  const k = ((navigator.languages && navigator.languages[0]) || navigator.language || 'en').toLowerCase().slice(0, 2);
  if (k === 'no' || k === 'nn') return 'nb';
  return k !== 'au' && LANG_NAMES[k] ? k : 'en';
}
const LANG = langCode(load('language', 'auto'));
const LOC = { da: 'da-DK', en: 'en-GB', de: 'de-DE', fr: 'fr-FR', sv: 'sv-SE', nb: 'nb-NO', nl: 'nl-NL' }[LANG];
document.documentElement.lang = LANG;
function tr(s, v) {
  const o = (window.TRANSLATIONS && TRANSLATIONS[LANG] && TRANSLATIONS[LANG][s]) || s;
  return v ? o.replace(/\{(\w+)\}/g, (_, k) => (v[k] === undefined ? '' : v[k])) : o;
}
const labelName = (l) => tr(cap(l));   // AI object (person, car …) in the selected language

// In the browser (PC version on the home network) the server is the address the page came from.
const WEB = location.protocol === 'http:' || location.protocol === 'https:';
const S = {
  // (the page may be under a sub-path, e.g. in Home Assistant's sidebar: /api/hassio_ingress/<key>/)
  server: WEB ? location.origin + location.pathname.replace(/\/[^/]*$/, '') : load('server', STD_SERVER),
  token: load('token', ''),
  user: load('user', null),
  opt: Object.assign({ boxes: true, liveBoxes: true, mobileSD: true, timeAxis: true }, load('opt', {})),
  seen: load('seen', 0),          // highest event id the user has seen
  lastCam: load('lastCam', ''),
  cams: [],                       // from /api/cams
  events: [],                     // latest events
};

// ---------- helpers ----------
const $ = (sel, root) => (root || document).querySelector(sel);
function el(tag, attrs, ...items) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === 'class') e.className = v;
    else if (k === 'style') e.style.cssText = v;
    else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else if (k === 'html') e.innerHTML = v;   // only for fixed icons from here
    else e.setAttribute(k, v === true ? '' : v);
  }
  for (const b of items.flat()) if (b !== null && b !== undefined && b !== false) e.append(b.nodeType ? b : document.createTextNode(String(b)));
  return e;
}
const ICON = {
  back: '<svg viewBox="0 0 24 24"><path d="M15 5l-7 7 7 7"/></svg>',
  eye: '<svg viewBox="0 0 128 128"><path d="M54.50,15.00 Q64.00,5.58 73.50,15.00 Q64.00,24.42 54.50,15.00 Z" fill="#FFFFFF" stroke="none"/><path d="M60.01,15.00 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M89.15,29.35 Q98.65,19.93 108.15,29.35 Q98.65,38.78 89.15,29.35 Z" fill="#FFFFFF" stroke="none"/><path d="M94.66,29.35 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M103.50,64.00 Q113.00,54.58 122.50,64.00 Q113.00,73.42 103.50,64.00 Z" fill="#FFFFFF" stroke="none"/><path d="M109.01,64.00 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M89.15,98.65 Q98.65,89.22 108.15,98.65 Q98.65,108.07 89.15,98.65 Z" fill="#FFFFFF" stroke="none"/><path d="M94.66,98.65 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M54.50,113.00 Q64.00,103.58 73.50,113.00 Q64.00,122.42 54.50,113.00 Z" fill="#FFFFFF" stroke="none"/><path d="M60.01,113.00 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M19.85,98.65 Q29.35,89.22 38.85,98.65 Q29.35,108.07 19.85,98.65 Z" fill="#FFFFFF" stroke="none"/><path d="M25.36,98.65 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M5.50,64.00 Q15.00,54.58 24.50,64.00 Q15.00,73.42 5.50,64.00 Z" fill="#FFFFFF" stroke="none"/><path d="M11.01,64.00 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M19.85,29.35 Q29.35,19.93 38.85,29.35 Q29.35,38.78 19.85,29.35 Z" fill="#FFFFFF" stroke="none"/><path d="M25.36,29.35 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M30.50,64.00 a33.50,33.50 0 1,0 67.00,0 a33.50,33.50 0 1,0 -67.00,0 Z" fill="#0D3634" stroke="none"/><path d="M64,64 L93.62,73.16 A31,31 0 0,1 69.28,94.55 Z" fill="#A8EDE4" stroke="#0D3634" stroke-width="1.6"/><path d="M64,64 L70.87,94.23 A31,31 0 0,1 40.19,83.85 Z" fill="#5CC8BB" stroke="#0D3634" stroke-width="1.6"/><path d="M64,64 L41.26,85.07 A31,31 0 0,1 34.90,53.30 Z" fill="#A8EDE4" stroke="#0D3634" stroke-width="1.6"/><path d="M64,64 L34.38,54.84 A31,31 0 0,1 58.72,33.45 Z" fill="#5CC8BB" stroke="#0D3634" stroke-width="1.6"/><path d="M64,64 L57.13,33.77 A31,31 0 0,1 87.81,44.15 Z" fill="#A8EDE4" stroke="#0D3634" stroke-width="1.6"/><path d="M64,64 L86.74,42.93 A31,31 0 0,1 93.10,74.70 Z" fill="#5CC8BB" stroke="#0D3634" stroke-width="1.6"/><path d="M52.84,64.00 a11.16,11.16 0 1,0 22.32,0 a11.16,11.16 0 1,0 -22.32,0 Z" fill="#0D3634" stroke="none"/><path d="M57.30,64.00 a6.70,6.70 0 1,0 13.39,0 a6.70,6.70 0 1,0 -13.39,0 Z" fill="#0B1514" stroke="none"/><path d="M64.56,60.65 a2.79,2.79 0 1,0 5.58,0 a2.79,2.79 0 1,0 -5.58,0 Z" fill="#FFFFFF" stroke="none"/></svg>',
  eyeColour: '<svg viewBox="0 0 128 128"><path d="M54.50,15.00 Q64.00,5.58 73.50,15.00 Q64.00,24.42 54.50,15.00 Z" fill="#FFFFFF" stroke="none"/><path d="M60.01,15.00 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M89.15,29.35 Q98.65,19.93 108.15,29.35 Q98.65,38.78 89.15,29.35 Z" fill="#FFFFFF" stroke="none"/><path d="M94.66,29.35 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M103.50,64.00 Q113.00,54.58 122.50,64.00 Q113.00,73.42 103.50,64.00 Z" fill="#FFFFFF" stroke="none"/><path d="M109.01,64.00 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M89.15,98.65 Q98.65,89.22 108.15,98.65 Q98.65,108.07 89.15,98.65 Z" fill="#FFFFFF" stroke="none"/><path d="M94.66,98.65 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M54.50,113.00 Q64.00,103.58 73.50,113.00 Q64.00,122.42 54.50,113.00 Z" fill="#FFFFFF" stroke="none"/><path d="M60.01,113.00 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M19.85,98.65 Q29.35,89.22 38.85,98.65 Q29.35,108.07 19.85,98.65 Z" fill="#FFFFFF" stroke="none"/><path d="M25.36,98.65 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M5.50,64.00 Q15.00,54.58 24.50,64.00 Q15.00,73.42 5.50,64.00 Z" fill="#FFFFFF" stroke="none"/><path d="M11.01,64.00 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M19.85,29.35 Q29.35,19.93 38.85,29.35 Q29.35,38.78 19.85,29.35 Z" fill="#FFFFFF" stroke="none"/><path d="M25.36,29.35 a3.99,3.99 0 1,0 7.98,0 a3.99,3.99 0 1,0 -7.98,0 Z" fill="#0D3634" stroke="none"/><path d="M30.50,64.00 a33.50,33.50 0 1,0 67.00,0 a33.50,33.50 0 1,0 -67.00,0 Z" fill="#0D3634" stroke="none"/><path d="M64,64 L93.62,73.16 A31,31 0 0,1 69.28,94.55 Z" fill="#A8EDE4" stroke="#0D3634" stroke-width="1.6"/><path d="M64,64 L70.87,94.23 A31,31 0 0,1 40.19,83.85 Z" fill="#5CC8BB" stroke="#0D3634" stroke-width="1.6"/><path d="M64,64 L41.26,85.07 A31,31 0 0,1 34.90,53.30 Z" fill="#A8EDE4" stroke="#0D3634" stroke-width="1.6"/><path d="M64,64 L34.38,54.84 A31,31 0 0,1 58.72,33.45 Z" fill="#5CC8BB" stroke="#0D3634" stroke-width="1.6"/><path d="M64,64 L57.13,33.77 A31,31 0 0,1 87.81,44.15 Z" fill="#A8EDE4" stroke="#0D3634" stroke-width="1.6"/><path d="M64,64 L86.74,42.93 A31,31 0 0,1 93.10,74.70 Z" fill="#5CC8BB" stroke="#0D3634" stroke-width="1.6"/><path d="M52.84,64.00 a11.16,11.16 0 1,0 22.32,0 a11.16,11.16 0 1,0 -22.32,0 Z" fill="#0D3634" stroke="none"/><path d="M57.30,64.00 a6.70,6.70 0 1,0 13.39,0 a6.70,6.70 0 1,0 -13.39,0 Z" fill="#0B1514" stroke="none"/><path d="M64.56,60.65 a2.79,2.79 0 1,0 5.58,0 a2.79,2.79 0 1,0 -5.58,0 Z" fill="#FFFFFF" stroke="none"/></svg>',
  gear: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/></svg>',
  power: '<svg viewBox="0 0 24 24"><path d="M12 3v8"/><path d="M6.3 7a8 8 0 1 0 11.4 0"/></svg>',
  camera: '<svg viewBox="0 0 24 24"><rect x="2" y="6" width="14" height="12" rx="2"/><path d="M16 10l6-3v10l-6-3z"/></svg>',
  clock: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>',
  search: '<svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="7"/><path d="M20 20l-4-4"/></svg>',
  full: '<svg viewBox="0 0 24 24"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg>',
  photo: '<svg viewBox="0 0 24 24"><path d="M4 8h3l2-3h6l2 3h3v11H4z"/><circle cx="12" cy="13" r="3.5"/></svg>',
  back10: '<svg viewBox="0 0 24 24"><path d="M4 12a8 8 0 1 0 3-6.2"/><path d="M4 4v4h4"/></svg>',
  forward10: '<svg viewBox="0 0 24 24"><path d="M20 12a8 8 0 1 1-3-6.2"/><path d="M20 4v4h-4"/></svg>',
  previous: '<svg viewBox="0 0 24 24"><path d="M6 5h2v14H6zM20 5v14L9 12z"/></svg>',
  next: '<svg viewBox="0 0 24 24"><path d="M16 5h2v14h-2zM4 5v14l11-7z"/></svg>',
  play: '<svg viewBox="0 0 24 24"><path d="M7 4l13 8-13 8z"/></svg>',
  download: '<svg viewBox="0 0 24 24"><path d="M12 3v12M7 10l5 5 5-5M5 20h14"/></svg>',
  flag: '<svg viewBox="0 0 24 24"><path d="M5 21V4M5 4h11l-2 4 2 4H5"/></svg>',
  trash: '<svg viewBox="0 0 24 24"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/></svg>',
  pause: '<svg viewBox="0 0 24 24"><path d="M6 5h4v14H6zM14 5h4v14h-4z"/></svg>',
};
function iconButton(name, label, onclick, cls) {
  return el('button', { type: 'button', class: cls || 'iconBtn', 'aria-label': label, onclick, html: ICON[name] });
}
const to = (n) => String(n).padStart(2, '0');
function fmtTime(t, withSec) { const d = new Date(t); return to(d.getHours()) + ':' + to(d.getMinutes()) + (withSec ? ':' + to(d.getSeconds()) : ''); }
function dayStart(t) { const d = new Date(t); d.setHours(0, 0, 0, 0); return d.getTime(); }
function dayName(t) {
  const d0 = dayStart(Date.now()), d = dayStart(t);
  if (d === d0) return tr('Today');
  if (d === d0 - DAY_MS) return tr('Yesterday');
  return new Date(t).toLocaleDateString(LOC, { weekday: 'long', day: 'numeric', month: 'long' });
}
function ago(t) {
  const s = (Date.now() - t) / 1000;
  if (s < 60) return tr('just now');
  if (s < 3600) return tr('{n} min ago', { n: Math.floor(s / 60) });
  if (s < 6 * 3600) { const h = Math.floor(s / 3600); return tr(h === 1 ? '{n} hour ago' : '{n} hours ago', { n: h }); }
  return dayName(t).replace(/^./, (c) => c.toUpperCase()) + ' ' + fmtTime(t);
}
function duration(ms) {
  const s = Math.max(1, Math.round(ms / 1000));
  return s < 60 ? s + ' s' : Math.floor(s / 60) + 'm' + to(s % 60) + 's';
}
const cap = (s) => s.charAt(0).toUpperCase() + s.slice(1);
function eventLabel(e) {
  // motion alone has no AI certainty: no percentage
  const what = (!e.labels || !e.labels.length) ? tr('Motion')
    : e.labels.map(labelName).join(', ') + (e.score ? ' ' + Math.round(e.score * 100) + ' %' : '');
  const zones = (e.zones || []).map((z) => zoneName(e.cam, z)).filter(Boolean);
  return zones.length ? what + ' · ' + zones.join(', ') : what;
}
const eventStart = (e) => new Date(e.start).getTime();
const eventEnd = (e) => (e.end && !e.end.startsWith('0001') ? new Date(e.end).getTime() : Date.now());
const camName = (id) => (S.cams.find((c) => c.id === id) || { name: id }).name;
const camById = (id) => S.cams.find((c) => c.id === id) || { id, name: id, width: 640, height: 480 };
// Mobile data? The Android app asks the phone; in the browser the Network Information API is used if available.
function onMobileData() {
  try { if (NATIVE && NATIVE.mobileData) return !!NATIVE.mobileData(); } catch (e) { /* older app */ }
  const c = navigator.connection;
  return !!(c && c.type === 'cellular');
}
const rot = (c) => (c && c.rotate === 180 ? 'transform:rotate(180deg);' : '');

// ---------- API ----------
class AuthError extends Error {}
async function api(apiPath, opts) {
  opts = opts || {};
  const h = { Authorization: 'Bearer ' + S.token };
  if (opts.body || (opts.method && opts.method !== 'GET')) h['Content-Type'] = 'application/json'; // the server requires JSON for changes
  let r;
  try {
    r = await fetch(S.server + apiPath, { method: opts.method || 'GET', headers: h, body: opts.body ? JSON.stringify(opts.body) : undefined, cache: 'no-store' });
  } catch (e) { throw new Error(tr('No connection to the server')); }
  if (r.status === 401 && !opts.noLogout) { logout(true); throw new AuthError(tr('login required')); }
  if (opts.blob) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.blob(); }
  let d = null;
  try { d = await r.json(); } catch (e) { /* empty response */ }
  if (!r.ok) throw new Error(d && d.error ? tr(d.error) : 'HTTP ' + r.status);  // the server's error texts are English = keys
  return d;
}

// Images require login: fetched as a blob and shown via an object URL.
const blobCache = new Map(); // apiPath -> url (thumbnails never change)
async function loadImage(img, apiPath, cache) {
  if (cache && blobCache.has(apiPath)) { img.src = blobCache.get(apiPath); return true; }
  try {
    const b = await api(apiPath, { blob: true });
    const u = URL.createObjectURL(b);
    if (cache) {
      blobCache.set(apiPath, u);
      if (blobCache.size > 300) { const [k, v] = blobCache.entries().next().value; URL.revokeObjectURL(v); blobCache.delete(k); }
    } else if (img.dataset.blob) URL.revokeObjectURL(img.dataset.blob);
    if (!cache) img.dataset.blob = u;
    img.src = u;
    return true;
  } catch (e) { return false; }
}

// Image + boxes in the camera's coordinates (sub stream), scaled with percentages.
function withBoxes(img, c, boxList, showText) {
  const w = c.width || 640, h = c.height || 480;
  const frameEl = el('div', { class: 'imageFrame', style: rot(c) + 'aspect-ratio:' + w + '/' + h });
  frameEl.append(img);
  if (S.opt.boxes && boxList) for (const b of boxList) frameEl.append(boxEl(b, w, h, showText));
  return frameEl;
}
function boxEl(b, w, h, showText) {
  const d = el('div', { class: 'box', style: `left:${b.x1 / w * 100}%;top:${b.y1 / h * 100}%;width:${(b.x2 - b.x1) / w * 100}%;height:${(b.y2 - b.y1) / h * 100}%` });
  if (showText) d.append(el('span', {}, labelName(b.label) + ' ' + Math.round(b.conf * 100) + ' %'));
  return d;
}

// ---------- navigation ----------
const stack = [];       // [{name, p}]
let active = null;      // {name, p, cleanup: [functions]}
const SCREENS = {};

function go(name, p, replaceTop) {
  if (active) { active.cleanup.forEach((f) => { try { f(); } catch (e) { /* */ } }); if (!replaceTop) stack.push({ name: active.name, p: active.p }); }
  active = { name, p: p || {}, cleanup: [] };
  const root = $('#screen');
  root.replaceChildren();
  root.dataset.screen = name; // CSS: e.g. the settings pages as a card
  // The bottom menu is shown on all pages except login (and hidden in fullscreen via CSS).
  // Sub-pages highlight the main page they belong to.
  $('#nav').hidden = name === 'login';
  const navSelected = name === 'camera' ? 'timeline' : name === 'playback' ? 'events'
    : (name.startsWith('set_') || name === 'log') ? 'settings' : name;
  for (const b of document.querySelectorAll('#nav button')) b.classList.toggle('selected', b.dataset.go === navSelected);
  NATIVE && NATIVE.keepAwake(name === 'camera' || name === 'playback' || name === 'live' || name === 'overview');
  SCREENS[name](root, active.p, active);
}
function back() {
  if (document.body.classList.contains('full')) { setFullscreen(false); return true; }
  const previous = stack.pop();
  if (!previous) return false;
  const previousScreen = active; active = null;
  previousScreen.cleanup.forEach((f) => { try { f(); } catch (e) { /* */ } });
  active = null;
  go(previous.name, previous.p, true);
  return true;
}
window.uiBack = back;
function navGo(name) {
  stack.length = 0;
  if (name === 'timeline') {
    const id = (S.cams.find((c) => c.id === S.lastCam) || S.cams[0] || {}).id;
    if (!id) return;
    go('overview', {}, true); go('camera', { cam: id });
    return;
  }
  go(name, {}, true);
}
// interval: repeats f while the screen is open – but not while the page is hidden (another tab, the app in the background,
// the screen off): then nothing is fetched, and everything is fetched at once when it is shown again.
const running = new Set();
// MOUSE: a pointer that can hover (a PC) – the help texts say "click" instead of "tap"
const MOUSE = window.matchMedia && matchMedia('(hover: hover) and (pointer: fine)').matches;
function interval(ctx, f, ms) {
  f();
  const t = setInterval(() => { if (!document.hidden) f(); }, ms);
  running.add(f);
  ctx.cleanup.push(() => { clearInterval(t); running.delete(f); });
}
document.addEventListener('visibilitychange', () => { if (!document.hidden) for (const f of [...running]) f(); });
function setFullscreen(until) {
  document.body.classList.toggle('full', until);
  if (NATIVE) NATIVE.setFullscreen(until);
  else if (until && document.documentElement.requestFullscreen) document.documentElement.requestFullscreen().catch(() => {});
  else if (!until && document.fullscreenElement) document.exitFullscreen().catch(() => {});
}
// Esc in the browser closes fullscreen
document.addEventListener('fullscreenchange', () => { if (!document.fullscreenElement) document.body.classList.remove('full'); });

function topBar(heading2, opts) {
  opts = opts || {};
  const h = el('header', { class: 'top' + (opts.back ? ' m-back' : '') });
  if (opts.back) h.append(iconButton('back', tr('Back'), () => back()));
  else if (opts.logo) h.append(el('span', { class: 'logo', html: ICON.eye }));
  const t = el('h1', {}, heading2);
  if (opts.under) t.append(el('span', { class: 'under' }, opts.under));
  h.append(t);
  for (const x of opts.right || []) h.append(x);
  return h;
}

// ---------- data shared by several screens ----------
async function fetchCameras() {
  try { S.cams = await api('/api/cams'); } catch (e) { /* keep the last one */ }
  return S.cams;
}
async function fetchEvents() {
  try { S.events = await api('/api/events?limit=100'); } catch (e) { /* */ }
  updateBadge();
  return S.events;
}
function updateBadge() {
  const n = S.events.filter((e) => e.id > S.seen).length;
  const b = $('#navBadge');
  b.hidden = n === 0;
  b.textContent = n > 99 ? '99+' : n;
}
function markSeen() {
  const max = S.events.reduce((m, e) => Math.max(m, e.id), S.seen);
  if (max > S.seen) { S.seen = max; save('seen', max); }
  updateBadge();
}

// ---------- camera tile with a refreshing still image ----------
function tile(c, ctx, large) {
  const img = el('img', { alt: '', style: rot(c), draggable: 'false' });
  const name = el('span', { class: 'name' }, c.name);
  const motionTag = el('span', { class: 'motionTag' }, tr('MOTION'));
  const homeTag = el('span', { class: 'motionTag homeTag', title: tr('No events while you are home') }, tr('HOME'));
  const noSignal = el('span', { class: 'noSignal' }, c.optional ? tr('Switched off') : tr('No signal'));
  const f = el('button', { type: 'button', class: 'tile' + (c.height > c.width ? ' tall' : ''), 'data-cam': c.id, 'aria-label': tr('Open {name}', { name: c.name }),
    onclick: () => go('camera', { cam: c.id }) }, img, name, noSignal);
  // A tile scrolled out of sight does not fetch new images (many cameras on a phone) – it does again as soon as it is seen.
  let seen = true;
  const refresh = async () => {
    const cc = camById(c.id);
    noSignal.hidden = cc.online;
    if (cc.motion) name.append(motionTag); else motionTag.remove();
    if (cc.events_off) name.append(homeTag); else homeTag.remove();
    if (seen && cc.online && (await loadImage(img, '/api/cams/' + c.id + '/snapshot.jpg'))) noSignal.hidden = true;
  };
  if (window.IntersectionObserver) {
    const io = new IntersectionObserver((es) => {
      const was = seen;
      seen = es[es.length - 1].isIntersecting;
      if (seen && !was) refresh();
    });
    io.observe(f);
    ctx.cleanup.push(() => io.disconnect());
  }
  interval(ctx, refresh, large ? 1500 : 2000);
  ctx.cleanup.push(() => img.dataset.blob && URL.revokeObjectURL(img.dataset.blob));
  return f;
}

// opts.time: the clock time instead of the camera's name (a list for one camera); opts.lazy: the picture is only fetched
// when the row comes into sight (a day can have many events); opts.current: the event being shown.
function eventRowEl(e, compact, onclick, opts) {
  opts = opts || {};
  const c = camById(e.cam);
  const img = el('img', { alt: '' });
  const frame = withBoxes(img, c, e.boxes, false);
  const mini = el('span', { class: 'thumbnail' }, frame);
  if (e.id > S.seen && !compact) mini.append(el('span', { class: 'fresh', 'aria-label': tr('New') }));
  if (e.keep) mini.append(el('span', { class: 'protected', 'aria-label': tr('Protected'), html: ICON.flag }));
  const path = '/api/events/' + e.id + '/thumb.jpg';
  if (opts.lazy && window.IntersectionObserver && !blobCache.has(path)) {
    const io = new IntersectionObserver((es) => { if (es.some((x) => x.isIntersecting)) { io.disconnect(); loadImage(img, path, true); } }, { rootMargin: '200px' });
    io.observe(mini);
  } else loadImage(img, path, true);
  const text = el('span', { class: 'eventText' },
    el('b', {}, opts.time ? fmtTime(eventStart(e), true) : camName(e.cam)),
    el('span', { class: 'd' }, opts.time ? duration(eventEnd(e) - eventStart(e)) : ago(eventStart(e)) + (compact ? ' · ' + duration(eventEnd(e) - eventStart(e)) : '')),
    compact ? null : el('span', { class: 'd' }, duration(eventEnd(e) - eventStart(e))),
    el('span', { class: 'l' }, eventLabel(e)));
  const row = el('button', { type: 'button', class: 'eventRow' + (compact ? ' compact' : '') + (opts.current ? ' current' : ''), onclick }, mini, text);
  if (opts.current) row.setAttribute('aria-current', 'true');
  return row;
}
// dayEventRows: the events of a day as rows with pictures (the column on the right on a PC)
function dayEventRows(evs, currentId) {
  const ids = evs.map((x) => x.id);
  return evs.length ? evs.map((e) => eventRowEl(e, true, () => go('playback', { id: e.id, list: ids, ev: e }, currentId !== undefined), { time: true, lazy: true, current: e.id === currentId }))
    : [el('p', { class: 'hint' }, tr('No events'))];
}

// ---------- screens ----------
SCREENS.login = (root) => {
  const errorText = el('p', { class: 'errorText', role: 'alert' });
  const userField = el('input', { type: 'text', autocomplete: 'username', autocapitalize: 'none', value: (S.user && S.user.name) || '' });
  const pw = el('input', { type: 'password', autocomplete: 'current-password' });
  const server = el('input', { type: 'url', value: S.server });
  const serverField = el('label', { class: 'field', hidden: true }, 'Server', server);
  const btn = el('button', { type: 'submit', class: 'btn green', style: 'min-height:52px;font-size:18px;letter-spacing:2px;margin-top:10px' }, tr('LOG IN'));
  const form = el('form', { onsubmit: async (ev) => {
    ev.preventDefault();
    errorText.textContent = '';
    btn.disabled = true; btn.textContent = tr('Logging in …');
    S.server = server.value.trim().replace(/\/+$/, '') || STD_SERVER;
    try {
      const r = await fetch(S.server + '/api/login', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ user: userField.value.trim(), password: pw.value, device: navigator.userAgent.includes('Android') ? 'Android-app' : 'browser' }) });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(d.error ? tr(d.error) : tr('Login failed'));
      S.token = d.token; S.user = d.user;
      save('server', S.server); save('token', S.token); save('user', S.user);
      NATIVE && NATIVE.login(S.server, S.token);
      start();
    } catch (e) {
      errorText.textContent = e.message === 'Failed to fetch' ? tr('No connection to the server') : e.message;
      btn.disabled = false; btn.textContent = tr('LOG IN');
    }
  } },
  el('label', { class: 'field' }, tr('User'), userField),
  el('label', { class: 'field' }, tr('Password'), pw),
  serverField, errorText, btn,
  WEB ? null : el('button', { type: 'button', class: 'server', onclick: () => { serverField.hidden = !serverField.hidden; } }, tr('Server') + ': ' + S.server.replace(/^https?:\/\//, '')),
  el('p', { class: 'hint', style: 'text-align:center;color:#D3F0EC' }, tr('You stay logged in on this device')));
  root.append(el('div', { class: 'login' },
    el('div', { class: 'head', html: ICON.eyeColour + '<h1>ARGUSSEN</h1><small>' + tr('CAMERA SYSTEM') + '</small>' }), form));
};

// The first start of a standalone ArgusSen: no users yet – create the first administrator (only from the local network).
SCREENS.setup = (root) => {
  const errorText = el('p', { class: 'errorText', role: 'alert' });
  const userField = el('input', { type: 'text', autocomplete: 'username', autocapitalize: 'none', value: 'admin' });
  const pw = el('input', { type: 'password', autocomplete: 'new-password' });
  const pw2 = el('input', { type: 'password', autocomplete: 'new-password' });
  const btn = el('button', { type: 'submit', class: 'btn green', style: 'min-height:52px;font-size:18px;margin-top:10px' }, tr('Create administrator'));
  const form = el('form', { onsubmit: async (ev) => {
    ev.preventDefault();
    errorText.textContent = '';
    if (pw.value !== pw2.value) { errorText.textContent = tr('The two passwords are not the same'); return; }
    btn.disabled = true;
    try {
      const r = await fetch(S.server + '/api/setup', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ user: userField.value.trim(), password: pw.value, device: 'browser' }) });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(d.error ? tr(d.error) : tr('Login failed'));
      S.token = d.token; S.user = d.user;
      save('token', S.token); save('user', S.user);
      start();
    } catch (e) {
      errorText.textContent = e.message === 'Failed to fetch' ? tr('No connection to the server') : e.message;
      btn.disabled = false;
    }
  } },
  el('p', { class: 'hint do' }, tr('Welcome to ArgusSen. Create the first administrator – it can add cameras, users and change all settings.')),
  el('label', { class: 'field' }, tr('User'), userField),
  el('label', { class: 'field' }, tr('Password (at least 8 characters)'), pw),
  el('label', { class: 'field' }, tr('Repeat the password'), pw2),
  errorText, btn);
  root.append(el('div', { class: 'login' },
    el('div', { class: 'head', html: ICON.eyeColour + '<h1>ARGUSSEN</h1><small>' + tr('CAMERA SYSTEM') + '</small>' }), form));
};

SCREENS.overview = (root, p, ctx) => {
  const online = el('span', { class: tr('online') });
  const grid = el('div', { class: 'grid' });
  const list = el('div', { style: 'display:flex;flex-direction:column;gap:6px' });
  root.append(topBar('ARGUSSEN', { logo: true, right: [online] }),
    el('div', { class: 'page' }, el('div', { class: 'content' },
      el('div', { class: 'heading' }, el('h2', {}, tr('Live')), el('span', { class: 'hint' }, MOUSE ? tr('Click to open · hold to move') : tr('Tap to open · hold to move'))),
      grid,
      el('div', { class: 'heading', style: 'margin-top:6px' }, el('h2', {}, tr('Latest events')),
        el('button', { type: 'button', class: 'link', onclick: () => navGo('events') }, tr('See all'))),
      list)));
  let drawn = null; // null = not drawn yet (an empty list must also be drawn)
  const arrange = arrangeGrid(grid, root.querySelector('.heading'), (key) => { drawn = key; }); // everybody: their own order
  interval(ctx, async () => {
    if (arrange && arrange.active()) return; // do not redraw while the cameras are being moved
    const cams = await fetchCameras();
    const counted = cams.filter((c) => c.online || !c.optional);   // cameras that are off and allowed to be off do not count
    const n = counted.filter((c) => c.online).length;
    if (!S.cams.length) online.replaceChildren(); // no cameras yet: nothing to count
    else online.replaceChildren(el('span', { class: 'dot' + (n < counted.length ? ' off' : '') }), tr('{n} of {m} online', { n, m: counted.length }));
    const camKey = cams.map((c) => c.id).join();
    if (camKey !== drawn) {
      drawn = camKey;
      grid.replaceChildren(...(cams.length ? cams.map((c) => tile(c, ctx, false))
        : [el('p', { class: 'empty', style: 'grid-column:1/-1' }, S.user && S.user.admin
          ? tr('No cameras yet – add your first under Settings → Cameras.') : tr('You do not have access to any cameras yet.'))]));
    }
  }, 2000);
  let lastOne = null;
  interval(ctx, async () => {
    const evs = (await fetchEvents()).slice(0, 5);
    const n = evs.map((e) => e.id + ':' + e.score + ':' + e.end).join();
    if (n === lastOne) return;
    lastOne = n;
    list.replaceChildren(...(evs.length ? evs.map((e) => eventRowEl(e, true, () => go('playback', { id: e.id, list: S.events.map((x) => x.id) })))
      : [el('p', { class: 'empty' }, tr('No events yet'))]));
  }, 15000);
};

// arrangeGrid: administrators hold a camera in the overview for half a second, then drag the cameras into a new
// order (saved when one is dropped – it is the order for everybody). "Done" ends it.
function arrangeGrid(grid, heading, onSaved) {
  // hold a camera, drag it and let go: the new order is saved and that is it (no "Done" to press)
  let on = false, drag = null, timer = null, start = null, moved = false, swallow = 0;
  const hint = heading.querySelector('.hint');
  const normal = hint.textContent; // "tap to open · hold to move" (administrators)
  const setOn = (v) => {
    on = v; grid.classList.toggle('arrange', v);
    hint.textContent = v ? tr('Drag the cameras into place') : normal;
  };
  const tileOf = (t) => t && t.closest && t.closest('.tile');
  const save = async () => {
    const order = [...grid.querySelectorAll('.tile')].map((t) => t.dataset.cam);
    try {
      // the user's own order (only for them); the administrators' default is in Settings → Cameras → Change order
      await api('/api/me/order', { method: 'PUT', body: { ids: order } });
      onSaved((await fetchCameras()).map((c) => c.id).join());
    } catch (e) { alert(e.message); }
  };
  grid.addEventListener('contextmenu', (ev) => { if (tileOf(ev.target)) ev.preventDefault(); });
  grid.addEventListener('pointerdown', (ev) => {
    const t = tileOf(ev.target);
    if (!t || !t.dataset.cam) return;
    start = { x: ev.clientX, y: ev.clientY, id: ev.pointerId }; moved = false;
    const begin = () => { drag = t; t.classList.add('dragging'); try { t.setPointerCapture(ev.pointerId); } catch (e) { /* */ } };
    timer = setTimeout(() => { timer = null; setOn(true); if (navigator.vibrate) navigator.vibrate(20); begin(); }, 500);
  });
  grid.addEventListener('pointermove', (ev) => {
    if (timer && start && Math.hypot(ev.clientX - start.x, ev.clientY - start.y) > 8) { clearTimeout(timer); timer = null; }
    if (!drag) return;
    moved = moved || Math.hypot(ev.clientX - start.x, ev.clientY - start.y) > 8;
    drag.style.pointerEvents = 'none';
    const over = tileOf(document.elementFromPoint(ev.clientX, ev.clientY));
    drag.style.pointerEvents = '';
    if (over && over !== drag && over.parentNode === grid) {
      const tiles = [...grid.children];
      grid.insertBefore(drag, tiles.indexOf(over) > tiles.indexOf(drag) ? over.nextSibling : over);
    }
  });
  // no scrolling while a camera is dragged (Android WebView/Chrome)
  grid.addEventListener('touchmove', (ev) => { if (drag) ev.preventDefault(); }, { passive: false });
  const end = () => {
    if (timer) { clearTimeout(timer); timer = null; }
    if (!drag) return;
    drag.classList.remove('dragging');
    drag = null;
    setOn(false);
    swallow = Date.now(); // the click the browser sends after letting go must not open the camera
    if (moved) save();
  };
  grid.addEventListener('pointerup', end);
  grid.addEventListener('pointercancel', end);
  // while arranging (and just after letting go), a tap does not open the camera
  grid.addEventListener('click', (ev) => { if (on || Date.now() - swallow < 500) { ev.stopPropagation(); ev.preventDefault(); } }, true);
  return { active: () => on || !!drag };
}

SCREENS.live = (root, p, ctx) => {
  const grid = el('div', { class: 'grid large' });
  const page = el('div', { class: 'page' }, el('div', { class: 'content' }, grid));
  root.append(topBar(tr('Live')), page);
  root.style.maxWidth = 'none'; // the live view may use the whole width of a big screen
  ctx.cleanup.push(() => { root.style.maxWidth = ''; });
  // a big screen (PC): all cameras fit the screen without scrolling – the number of columns that gives the biggest
  // pictures is chosen. A phone keeps one camera per row (scroll), where each picture is as big as possible.
  const wide = window.matchMedia('(min-width: 800px)');
  const fit = () => {
    const n = grid.children.length;
    if (!wide.matches || !n) { grid.style.gridTemplateColumns = ''; grid.style.justifyContent = ''; return; }
    const gap = 10, pad = 24;
    const W = page.clientWidth - pad, H = page.clientHeight - pad;
    let best = { w: 0, cols: 1 };
    for (let cols = 1; cols <= n; cols++) {
      const rows = Math.ceil(n / cols);
      const w = Math.min((W - gap * (cols - 1)) / cols, ((H - gap * (rows - 1)) / rows) * 4 / 3);
      if (w > best.w) best = { w, cols };
    }
    grid.style.gridTemplateColumns = 'repeat(' + best.cols + ', ' + Math.floor(best.w) + 'px)';
    grid.style.justifyContent = 'center';
  };
  const ro = new ResizeObserver(fit);
  ro.observe(page);
  wide.addEventListener('change', fit);
  ctx.cleanup.push(() => { ro.disconnect(); wide.removeEventListener('change', fit); });
  let drawn = null;
  interval(ctx, async () => {
    const cams = await fetchCameras();
    const k = cams.map((c) => c.id).join();
    if (k !== drawn) { drawn = k; grid.replaceChildren(...cams.map((c) => tile(c, ctx, true))); fit(); }
  }, 2000);
};

SCREENS.events = (root, p, ctx) => {
  // A phone: one list, newest first. A PC: one shared time axis down the middle with the events as cards on both sides –
  // every other event on the other side (zigzag, the cards slot in between), every camera has its own colour, a long
  // pause is shown on the axis.
  // "Pick day" shows the events up to the end of that day; "Select" marks several events to protect or delete at once.
  const st = Object.assign({ filter: 'All', search: '', cam: '', day: null }, p.filter ? p : {});
  const keep = () => Object.assign(p, { filter: st.filter, search: st.search, cam: st.cam, day: st.day });
  const wide = window.matchMedia('(min-width: 800px)');
  const PAGE = 100;
  const admin = S.user && S.user.admin;
  // ---- the filters ----
  const chips = el('div', { class: 'chips' });
  const search = el('input', { type: 'search', placeholder: tr('Search camera or zone'), 'aria-label': tr('Search camera or zone'), value: st.search,
    oninput: () => { st.search = search.value; keep(); draw(); } });
  const camSel = el('select', { class: 'camSelect', 'aria-label': tr('Camera'), onchange: () => { st.cam = camSel.value; keep(); reload(); } });
  const drawCamSel = () => {
    camSel.replaceChildren(el('option', { value: '' }, tr('All cameras')), ...S.cams.map((c) => el('option', { value: c.id }, c.name)));
    if (st.cam && !S.cams.some((c) => c.id === st.cam)) st.cam = '';
    camSel.value = st.cam;
    camSel.hidden = S.cams.length < 2;
  };
  const dateInput = el('input', { type: 'date', class: 'datePicker', 'aria-label': tr('Pick day') });
  dateInput.max = new Date().toISOString().slice(0, 10);
  dateInput.onchange = () => { if (!dateInput.value) return; const [y, m, d] = dateInput.value.split('-').map(Number); st.day = new Date(y, m - 1, d).getTime(); keep(); reload(); };
  const dayChip = el('button', { type: 'button', class: 'chip' });
  const drawDayChip = () => {
    if (st.day === null) { dayChip.textContent = tr('Pick day'); dayChip.classList.remove('selected'); dayChip.onclick = () => (dateInput.showPicker ? dateInput.showPicker() : dateInput.click()); dayChip.removeAttribute('aria-label'); return; }
    dayChip.textContent = tr('Up to {date}', { date: new Date(st.day).toLocaleDateString(LOC, { day: 'numeric', month: 'short' }) }) + '  ×';
    dayChip.classList.add('selected'); dayChip.setAttribute('aria-label', tr('Show the newest again'));
    dayChip.onclick = () => { st.day = null; keep(); reload(); };
  };
  const selectChip = el('button', { type: 'button', class: 'chip', onclick: () => setSelecting(!selecting) }, tr('Select'));
  const list = el('div');
  const page = el('div', { class: 'page' }, list);
  const bar = el('div', { class: 'bulkBar', hidden: true });
  root.append(topBar(tr('Events')),
    el('div', { class: 'filters' }, el('div', { class: 'searchRow' }, el('label', { class: 'search', html: ICON.search }, search), camSel),
      el('div', { class: 'filterRow' }, chips, el('div', { class: 'chips' }, dayChip, selectChip)), dateInput), page, bar);
  // ---- the data: one list per key ('' = all cameras, or a camera's id) ----
  let lists = p._lists || {};
  ctx.cleanup.push(() => { p._lists = lists; p._scroll = page.scrollTop; });
  const keys = () => [st.cam || ''];
  const size = () => PAGE;
  async function load(key, older) {
    const L = lists[key] || (lists[key] = { items: [], more: false });
    const before = older && L.items.length ? eventStart(L.items[L.items.length - 1]) : (st.day !== null ? st.day + DAY_MS : 0);
    const q = '/api/events?limit=' + size() + (key ? '&cam=' + encodeURIComponent(key) : '') + (st.filter === 'Protected' ? '&keep=1' : '') + (before ? '&before=' + before : '');
    try { const got = await api(q); L.items = older ? L.items.concat(got) : got; L.more = got.length >= size(); } catch (e) { /* keep what we have */ }
  }
  async function reload() {
    lists = {}; setSelecting(false); drawDayChip(); page.scrollTop = 0;
    await Promise.all(keys().map((k) => load(k, false)));
    draw();
  }
  // ---- selecting several ----
  let selecting = false;
  const picked = new Map(); // id -> event
  function setSelecting(on) {
    selecting = on; picked.clear();
    selectChip.classList.toggle('selected', on);
    root.classList.toggle('picking', on);
    drawBar(); draw();
  }
  function drawBar() {
    bar.hidden = !selecting;
    if (!selecting) return;
    const n = picked.size;
    const protect = el('button', { type: 'button', class: 'btn grey', disabled: !n, html: ICON.flag + tr('Protect'), onclick: async () => {
      for (const e of picked.values()) if (!e.keep) { try { await protectEvent(e); } catch (err) { alert(err.message); break; } }
      setSelecting(false);
    } });
    const del = admin ? el('button', { type: 'button', class: 'btn grey', disabled: !n, html: ICON.trash + tr('Delete'), onclick: async () => {
      if (!confirm(tr('Delete {n} events? This cannot be undone.', { n }))) return;
      const gone = new Set();
      for (const e of picked.values()) { try { await api('/api/events/' + e.id, { method: 'DELETE' }); gone.add(e.id); } catch (err) { alert(err.message); break; } }
      for (const L of Object.values(lists)) L.items = L.items.filter((x) => !gone.has(x.id));
      S.events = S.events.filter((x) => !gone.has(x.id));
      setSelecting(false);
    } }) : null;
    bar.replaceChildren(el('span', { class: 'bulkCount' }, n ? tr('{n} selected', { n }) : tr('Tap the events to select them')), protect, del,
      el('button', { type: 'button', class: 'btn grey', onclick: () => setSelecting(false) }, tr('Cancel')));
  }
  // ---- drawing ----
  const drawChips = () => chips.replaceChildren(...['All', ...LABELS, 'Protected'].map((n) =>
    el('button', { type: 'button', class: 'chip' + (st.filter === n ? ' selected' : ''), 'aria-pressed': st.filter === n ? 'true' : 'false',
      onclick: () => { const reloadNeeded = (n === 'Protected') !== (st.filter === 'Protected'); st.filter = n; keep(); drawChips(); if (reloadNeeded) reload(); else draw(); } }, tr(n))));
  const matches = (e) => {
    const q = st.search.trim().toLowerCase();
    return (st.filter === 'All' || st.filter === 'Protected' || (e.labels || []).includes(st.filter.toLowerCase()))
      && (!q || camName(e.cam).toLowerCase().includes(q) || (e.zones || []).some((z) => zoneName(e.cam, z).toLowerCase().includes(q)));
  };
  // one event: a row with its picture; in select mode a tap marks it
  const item = (e, shown, compact, opts) => {
    const open = () => {
      if (selecting) { if (picked.has(e.id)) picked.delete(e.id); else picked.set(e.id, e); row.classList.toggle('picked', picked.has(e.id)); drawBar(); return; }
      go('playback', { id: e.id, list: shown.map((x) => x.id) });
    };
    const row = el('div', { class: 'eventItem' + (picked.has(e.id) ? ' picked' : '') },
      eventRowEl(e, compact, open, opts || { lazy: true }),
      eventIcons(e, () => { for (const L of Object.values(lists)) L.items = L.items.filter((x) => x.id !== e.id); draw(); }));
    return row;
  };
  // the events with a heading per day
  const withDays = (evs, compact) => {
    const out = [];
    let day = '';
    for (const e of evs) {
      const d = dayName(eventStart(e));
      if (d !== day) { day = d; out.push(el('h2', { class: 'small day' }, d)); }
      out.push(item(e, evs, compact));
    }
    return out;
  };
  const moreButton = (key) => el('button', { type: 'button', class: 'moreBtn', onclick: async (ev) => { ev.target.disabled = true; await load(key, true); draw(); } }, tr('Load older events'));
  // the PC view: legend (a camera's colour; a tap shows only that camera), the axis with time, days and long pauses
  const TLV_COLOURS = ['#5CC8BB', '#F59A3C', '#8AB4F8', '#EF8A80', '#BA9BE6', '#F2D165', '#7BD88F', '#F28CC4'];
  const camColour = (id) => TLV_COLOURS[Math.max(0, S.cams.findIndex((c) => c.id === id)) % TLV_COLOURS.length];
  const gapText = (ms) => { const m = Math.round(ms / 60000); return m < 60 ? m + ' min' : tr('{h} h {m} min', { h: Math.floor(m / 60), m: m % 60 }); };
  function timelineView(evs, L, key) {
    const legend = el('div', { class: 'tlvLegend' },
      el('button', { type: 'button', class: 'chip' + (st.cam ? '' : ' selected'), 'aria-pressed': st.cam ? 'false' : 'true',
        onclick: () => { if (!st.cam) return; st.cam = ''; keep(); drawCamSel(); reload(); } }, tr('All')),
      ...S.cams.map((c) => el('button', { type: 'button', class: 'chip tlvKey' + (st.cam === c.id ? ' selected' : ''),
      style: '--c:' + camColour(c.id), 'aria-pressed': st.cam === c.id ? 'true' : 'false',
      onclick: () => { st.cam = st.cam === c.id ? '' : c.id; keep(); drawCamSel(); reload(); } }, el('span', { class: 'tlvDot' }), c.name)));
    const rows = [];
    let day = '', prev = null, lastSide = null; // lastSide: the side of the card just above (null after a day or a pause)
    for (const e of evs) {
      const d = dayName(eventStart(e));
      if (d !== day) { day = d; prev = null; lastSide = null; rows.push(el('div', { class: 'tlvDay' }, el('span', {}, d))); }
      else if (prev && eventStart(prev) - eventEnd(e) > 3600000) { lastSide = null; rows.push(el('div', { class: 'tlvGap' }, el('span', {}, tr('{d} without events', { d: gapText(eventStart(prev) - eventEnd(e)) })))); }
      const left = lastSide === null ? true : !lastSide; // every other card on the other side
      const card = el('div', { class: 'tlvCard' }, item(e, evs, true, { lazy: true }));
      const side = el('div', { class: 'tlvSide ' + (left ? 'left' : 'right') }, ...(left ? [card, el('span', { class: 'tlvLink' })] : [el('span', { class: 'tlvLink' }), card]));
      // a card on the other side than the one above moves half a card up (zigzag): twice as many events on the screen
      const tuck = lastSide !== null && lastSide !== left;
      lastSide = left;
      rows.push(el('div', { class: 'tlvRow' + (st.cam ? ' one' : '') + (tuck ? ' tuck' : ''), style: '--c:' + camColour(e.cam) },
        left ? side : el('div'), el('div', { class: 'tlvTime' }, el('span', {}, fmtTime(eventStart(e), true))), left ? el('div') : side));
      prev = e;
    }
    if (!rows.length) rows.push(el('p', { class: 'empty' }, st.filter === 'Protected' ? tr('No protected events') : tr('No events match the filter')));
    return el('div', { class: 'tlvWrap' }, S.cams.length > 1 ? legend : null, el('div', { class: 'tlv' }, ...rows), L.more ? moreButton(key) : null);
  }
  function draw() {
    const key = keys()[0];
    const L = lists[key] || { items: [], more: false };
    const shown = L.items.filter(matches);
    if (wide.matches && S.opt.timeAxis) { list.replaceChildren(timelineView(shown, L, key)); return; } // the choice is in General
    list.replaceChildren(...(shown.length ? withDays(shown, false) : [el('p', { class: 'empty' }, st.filter === 'Protected' ? tr('No protected events') : L.items.length || st.cam || st.day !== null ? tr('No events match the filter') : tr('No events yet'))]),
      ...(L.more ? [moreButton(key)] : []));
  }
  const relayout = () => draw();
  wide.addEventListener('change', relayout);
  ctx.cleanup.push(() => wide.removeEventListener('change', relayout));
  drawChips(); drawCamSel(); drawDayChip();
  if (p._lists) { draw(); page.scrollTop = p._scroll || 0; } else reload();
  // new events every 15 s – only while the newest are shown and nothing is being selected
  let started = false;
  interval(ctx, async () => {
    if (!started) { started = true; return; } // the first round is the loading above
    await fetchCameras(); drawCamSel();
    await fetchEvents(); // the badge in the menu
    if (st.day !== null || selecting) return;
    const before = page.scrollHeight;
    await Promise.all(keys().filter((k) => !lists[k] || lists[k].items.length <= size()).map((k) => load(k, false)));
    draw();
    if (page.scrollTop > 0) page.scrollTop += page.scrollHeight - before; // new events at the top must not push the list down under the finger
  }, 15000);
  ctx.cleanup.push(markSeen, () => root.classList.remove('picking'));
};

// ---------- video ----------
// Player using hls.js; the login token is sent with every request.
function newPlayer(c, ctx) {
  const video = el('video', { style: rot(c), playsinline: true, muted: true, autoplay: true, poster: 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7' });
  video.muted = true;
  const overlay = el('div', { class: 'overlay', style: rot(c) });
  const msg = el('div', { class: 'msg', hidden: true });
  const clock = el('span', { class: 'clock', hidden: true });
  const buttons = el('div', { class: 'buttons' });
  const zoom = el('div', { class: 'zoom' }, video, overlay);
  const fullBack = el('button', { type: 'button', class: 'full-back', 'aria-label': tr('Close full screen'), html: ICON.back, onclick: () => setFullscreen(false) });
  const box = el('div', { class: 'player', style: 'aspect-ratio:' + (c.width || 4) + '/' + (c.height || 3) }, zoom, msg, clock, buttons, fullBack);
  zoomable(box, zoom);
  let hls = null;
  const A = { video, box, overlay, msg, clock, buttons, hls: null };
  A.notice = (t) => { msg.textContent = t || ''; msg.hidden = !t; };
  A.stop = () => {
    if (A.mse) { A.mse.abort(); A.mse = null; video.playbackRate = 1; }
    if (hls) { hls.destroy(); hls = null; A.hls = null; }
    A.frags = null; video.removeAttribute('src');
  };
  // Fast live view (server livemse.go): one long response with fragmented MP4, fed to the browser's MediaSource and kept
  // about a quarter of a second behind the newest frame. Answers false (and the caller uses HLS) when the browser or the
  // server cannot; onEnd is called if the stream ends later (e.g. the camera reconnects).
  A.playMSE = (url, onEnd) => new Promise((ok) => {
    A.stop();
    const MS = window.ManagedMediaSource || window.MediaSource;
    if (!MS || !window.ReadableStream || load('liveHLS', false)) { ok(false); return; }
    const ac = new AbortController();
    A.mse = ac;
    let started = false, failed = false;
    const fail = () => { if (failed) return; failed = true; if (A.mse === ac) { ac.abort(); A.mse = null; } if (!started) ok(false); };
    const startTimer = setTimeout(() => { if (!started) fail(); }, 8000);
    A.notice(tr('Loading video …'));
    (async () => {
      const r = await fetch(S.server + url, { headers: { Authorization: 'Bearer ' + S.token }, signal: ac.signal, cache: 'no-store' });
      const codec = r.headers.get('X-Codec');
      const type = 'video/mp4; codecs="' + codec + '"';
      if (!r.ok || !codec || !r.body || !MS.isTypeSupported(type)) { fail(); return; }
      const ms = new MS();
      if (window.ManagedMediaSource) video.disableRemotePlayback = true;
      video.src = URL.createObjectURL(ms);
      await new Promise((res) => ms.addEventListener('sourceopen', res, { once: true }));
      const sb = ms.addSourceBuffer(type);
      sb.mode = 'segments';
      const queue = [];
      const pump = () => {
        if (sb.updating || failed) return;
        const b = video.buffered;
        // trim what has been played (keep 10 s), so the buffer never fills up
        if (b.length && video.currentTime - b.start(0) > 30) { try { sb.remove(b.start(0), video.currentTime - 10); } catch (e) { /* next time */ } return; }
        if (!queue.length) return;
        let data = queue.shift();
        if (queue.length) { // append everything that is waiting in one go
          const all = new Uint8Array(data.length + queue.reduce((n, q) => n + q.length, 0));
          all.set(data, 0); let o = data.length;
          for (const q of queue.splice(0)) { all.set(q, o); o += q.length; }
          data = all;
        }
        try { sb.appendBuffer(data); } catch (e) { fail(); }
      };
      sb.addEventListener('updateend', () => {
        const b = video.buffered;
        if (b.length) {
          const end = b.end(b.length - 1), lag = end - video.currentTime;
          if (!started) {
            started = true; clearTimeout(startTimer);
            video.currentTime = Math.max(b.start(b.length - 1), end - 0.2);
            video.play().catch(() => {});
            A.notice(''); ok(true);
          } else if (lag > 1.5 || video.currentTime < b.start(b.length - 1)) {
            video.currentTime = end - 0.2; // far behind (the tab was hidden, slow network): jump to now
          } else {
            video.playbackRate = lag > 0.5 ? 1.15 : 1; // a little behind: catch up quietly
          }
        }
        pump();
      });
      video.addEventListener('error', fail, { once: true });
      const reader = r.body.getReader();
      for (;;) {
        const { value, done } = await reader.read();
        if (done || failed) break;
        queue.push(value);
        pump();
      }
      if (A.mse === ac && !failed) { A.mse = null; if (started && onEnd) onEnd(); else fail(); }
    })().catch(() => { if (A.mse === ac || !started) fail(); });
  });
  // play(url, live, at): at = wall-clock time (ms) to start at (recordings only)
  A.play = (url, live, at) => new Promise((ok) => {
    A.stop();
    A.notice(tr('Loading video …'));
    if (!window.Hls || !Hls.isSupported()) { A.notice(tr('This device cannot play the video')); ok(false); return; }
    hls = new Hls(Object.assign({
      xhrSetup: (xhr) => xhr.setRequestHeader('Authorization', 'Bearer ' + S.token),
      enableWorker: true, backBufferLength: 30,
    }, live ? { liveSyncDurationCount: 2, liveMaxLatencyDurationCount: 6, maxBufferLength: 8 } : { maxBufferLength: 30, startPosition: -1 }));
    A.hls = hls;
    let started = false;
    hls.on(Hls.Events.LEVEL_LOADED, (ev, d) => {
      if (!live) A.frags = d.details.fragments;
      if (started || live || !at) return;
      started = true;
      const fr = d.details.fragments;
      let pos = 0;
      for (const f of fr) if (f.programDateTime && f.programDateTime <= at) pos = f.start + (at - f.programDateTime) / 1000;
      hls.startLoad(pos);
      video.currentTime = pos;
    });
    hls.on(Hls.Events.FRAG_BUFFERED, () => { A.notice(''); });
    let mediaErrors = 0;
    const noCodec = () => { A.notice(tr('This browser cannot show this video (the camera probably sends H.265). Choose H.264 in the camera, or use a browser that supports H.265.')); ok(false); };
    hls.on(Hls.Events.ERROR, (ev, d) => {
      if (/codec/i.test(d.details || '') || (d.error && /codec|not supported/i.test(d.error.message || ''))) { if (hls) hls.destroy(); hls = null; A.hls = null; noCodec(); return; }
      if (!d.fatal) return;
      if (d.response && d.response.code === 404 && !live) { A.notice(tr('No recording here')); ok(false); return; }
      if (live && d.type === Hls.ErrorTypes.NETWORK_ERROR) { A.notice(c.optional && !camById(c.id).online ? tr('The camera is switched off') : tr('Waiting for the camera …')); setTimeout(() => { if (hls) { hls.loadSource(S.server + url); } }, 3000); return; }
      if (d.type === Hls.ErrorTypes.NETWORK_ERROR) { A.notice(tr('Connection trouble – retrying …')); setTimeout(() => hls && hls.startLoad(), 2000); }
      else if (d.type === Hls.ErrorTypes.MEDIA_ERROR) { if (++mediaErrors > 2) noCodec(); else hls.recoverMediaError(); }
      else { A.notice(tr('The video could not be played')); ok(false); }
    });
    hls.on(Hls.Events.MANIFEST_PARSED, () => { video.play().catch(() => {}); ok(true); });
    hls.loadSource(S.server + url);
    hls.attachMedia(video);
  });
  // wall-clock time of what is shown right now
  // Recordings: computed from the playlist's times (hls.js' playingDate drifts over many minute changes).
  A.frags = null;
  A.at = () => {
    if (!hls) return null;
    const ct = video.currentTime;
    if (A.frags && A.frags.length) {
      let f = A.frags[0];
      for (const x of A.frags) { if (x.start <= ct) f = x; else break; }
      if (f.programDateTime) return f.programDateTime + (ct - f.start) * 1000;
    }
    return hls.playingDate ? hls.playingDate.getTime() : null;
  };
  // the box the video actually fills (object-fit: contain)
  A.rect = () => {
    const bw = box.clientWidth, bh = box.clientHeight, vw = video.videoWidth || c.width, vh = video.videoHeight || c.height;
    const s = Math.min(bw / vw, bh / vh);
    return { x: (bw - vw * s) / 2, y: (bh - vh * s) / 2, w: vw * s, h: vh * s };
  };
  A.drawBoxes = (boxList) => {
    overlay.replaceChildren();
    if (!boxList || !boxList.length) return;
    const r = A.rect(), w = c.width || 640, h = c.height || 480;
    const frameEl = el('div', { style: `position:absolute;left:${r.x}px;top:${r.y}px;width:${r.w}px;height:${r.h}px` });
    for (const b of boxList) frameEl.append(boxEl(b, w, h, true));
    overlay.append(frameEl);
  };
  // Save image: the picture the video shows right now, in its full size (HD when HD is shown), turned like on screen
  A.saveImage = async () => {
    if (!video.videoWidth) return;
    const cv = document.createElement('canvas');
    const turn = c.rotate === 180;
    cv.width = video.videoWidth; cv.height = video.videoHeight;
    const g = cv.getContext('2d');
    if (turn) { g.translate(cv.width, cv.height); g.rotate(Math.PI); }
    g.drawImage(video, 0, 0);
    const t = new Date(A.at() || Date.now());
    const name = 'ArgusSen_' + (c.name || c.id).replace(/\s+/g, '_') + '_' + t.getFullYear() + '-' + to(t.getMonth() + 1) + '-' + to(t.getDate()) + '_' + to(t.getHours()) + '-' + to(t.getMinutes()) + '-' + to(t.getSeconds()) + '.jpg';
    const say = (txt) => { A.notice(txt); setTimeout(() => A.notice(''), 2500); };
    if (NATIVE) {
      if (!NATIVE.saveImage) { say(tr('Update the app to save images')); return; }
      say(NATIVE.saveImage(cv.toDataURL('image/jpeg', 0.92).split(',')[1], name) ? tr('Image saved') : tr('Could not save the image'));
      return;
    }
    cv.toBlob((b) => {
      if (!b) return;
      const a = el('a', { href: URL.createObjectURL(b), download: name }); document.body.append(a); a.click(); a.remove();
      setTimeout(() => URL.revokeObjectURL(a.href), 5000);
      say(tr('Image saved'));
    }, 'image/jpeg', 0.92);
  };
  buttons.append(iconButton('full', tr('Full screen'), () => setFullscreen(!document.body.classList.contains('full')), 'small-btn'),
    Object.assign(iconButton('photo', tr('Save image'), () => A.saveImage(), 'small-btn'), { title: tr('Save image') }));
  A.buttons = buttons;
  ctx.cleanup.push(() => { A.stop(); setFullscreen(false); });
  return A;
}

// wheelStepper: whole steps from the mouse wheel – one per notch of a mouse; a touchpad's small movements add up.
function wheelStepper() {
  let acc = 0;
  return (e) => {
    let d = Math.abs(e.deltaY) >= Math.abs(e.deltaX) ? e.deltaY : e.deltaX;
    if (e.deltaMode === 1) d *= 33; else if (e.deltaMode === 2) d *= 400;
    if (Math.abs(d) >= 50) { acc = 0; return Math.sign(d); } // a mouse: one step per notch
    acc += d;
    const n = Math.trunc(acc / 100);
    acc -= n * 100;
    return n;
  };
}

// Pinch to zoom, drag with one finger when zoomed in, double tap = zoom in/out; a PC: the mouse wheel zooms around the pointer.
function zoomable(box, inner) {
  let s = 1, x = 0, y = 0, lastTap = 0, moved = false;
  const pts = new Map();
  let start = null;
  const draw = () => {
    const w = box.clientWidth, h = box.clientHeight;
    const mx = w * (s - 1) / 2, my = h * (s - 1) / 2;
    x = Math.max(-mx, Math.min(mx, x)); y = Math.max(-my, Math.min(my, y));
    inner.style.transform = s === 1 ? '' : `translate(${x}px,${y}px) scale(${s})`;
    box.classList.toggle('zoomed', s > 1);
  };
  const mid = () => { const p = [...pts.values()]; return { x: (p[0].x + p[1].x) / 2, y: (p[0].y + p[1].y) / 2, d: Math.hypot(p[0].x - p[1].x, p[0].y - p[1].y) }; };
  // zoom around a point (px relative to the box centre)
  const zoomTo = (fresh, px, py) => {
    fresh = Math.max(1, Math.min(8, fresh));
    x = px - (px - x) * fresh / s; y = py - (py - y) * fresh / s; s = fresh;
    if (s === 1) { x = 0; y = 0; }
    draw();
  };
  const rel = (e) => { const r = box.getBoundingClientRect(); return { x: e.clientX - r.left - r.width / 2, y: e.clientY - r.top - r.height / 2 }; };
  box.addEventListener('pointerdown', (e) => {
    if (e.target.closest('button')) return;
    box.setPointerCapture(e.pointerId);
    pts.set(e.pointerId, { x: e.clientX, y: e.clientY });
    moved = false;
    if (pts.size === 2) { const m = mid(); start = { s, x, y, m, r: rel({ clientX: m.x, clientY: m.y }) }; }
  });
  box.addEventListener('pointermove', (e) => {
    const p = pts.get(e.pointerId);
    if (!p) return;
    const dx = e.clientX - p.x, dy = e.clientY - p.y;
    if (Math.abs(dx) + Math.abs(dy) > 3) moved = true;
    p.x = e.clientX; p.y = e.clientY;
    if (pts.size === 2 && start) {
      const m = mid();
      s = start.s; x = start.x; y = start.y;
      zoomTo(start.s * m.d / start.m.d, start.r.x, start.r.y);
      x += m.x - start.m.x; y += m.y - start.m.y; draw();
    } else if (pts.size === 1 && s > 1) { x += dx; y += dy; draw(); }
  });
  const endT = (e) => {
    if (!pts.has(e.pointerId)) return;
    pts.delete(e.pointerId);
    if (pts.size < 2) start = null;
    if (pts.size === 0 && !moved && e.type === 'pointerup') {
      const nowT = Date.now();
      if (nowT - lastTap < 300) { const r = rel(e); zoomTo(s > 1 ? 1 : 2.5, r.x, r.y); lastTap = 0; }
      else lastTap = nowT;
    }
  };
  box.addEventListener('pointerup', endT);
  box.addEventListener('pointercancel', endT);
  box.addEventListener('wheel', (e) => {
    if (e.target.closest('button')) return;
    let d = e.deltaY;
    if (e.deltaMode === 1) d *= 33; else if (e.deltaMode === 2) d *= 400;
    if (s === 1 && d >= 0) return; // not zoomed in: scrolling down still scrolls the page
    e.preventDefault();
    const r = rel(e);
    zoomTo(s * Math.pow(1.0015, -Math.max(-200, Math.min(200, d))), r.x, r.y);
  }, { passive: false });
  new ResizeObserver(() => draw()).observe(box);
}

// Live + timeline for one camera
// speedControl: "− 1× +" over the video, the same on the timeline and when playing an event: ½× … 16× (the most browsers play).
const SPEED_STEPS = [0.5, 1, 2, 4, 8, 16];
function speedControl(A, st, ctx) {
  const label = el('span', { class: 'speedVal', 'aria-live': 'polite' });
  let live = false;
  const minus = el('button', { type: 'button', class: 'small-btn', 'aria-label': tr('Slower') }, '−');
  const plus = el('button', { type: 'button', class: 'small-btn', 'aria-label': tr('Faster') }, '+');
  const apply = () => {
    const v = live ? 1 : st.speed;
    label.textContent = (v === 0.5 ? '½' : v) + '×';
    if (A.video.playbackRate !== v) { A.video.defaultPlaybackRate = v; A.video.playbackRate = v; }
    minus.disabled = v === SPEED_STEPS[0]; plus.disabled = v === SPEED_STEPS[SPEED_STEPS.length - 1];
  };
  const step = (d) => {
    const i = SPEED_STEPS.indexOf(st.speed);
    st.speed = SPEED_STEPS[Math.max(0, Math.min(SPEED_STEPS.length - 1, (i < 0 ? 1 : i) + d))];
    apply();
  };
  minus.onclick = () => step(-1);
  plus.onclick = () => step(1);
  A.video.addEventListener('loadedmetadata', apply);
  const box = el('div', { class: 'speedCtl' }, minus, label, plus);
  if (!SPEED_STEPS.includes(st.speed)) st.speed = 1;
  apply();
  // live video always plays at 1× (the control is hidden then)
  return { el: box, apply, setLive: (v) => { if (v === live && box.hidden === v) return; live = v; box.hidden = v; apply(); } };
}

SCREENS.camera = (root, p, ctx) => {
  const c = camById(p.cam);
  S.lastCam = c.id; save('lastCam', c.id);
  const st = { hd: p.hd !== undefined ? !!p.hd : true, at: p.at || null, day: p.day || dayStart(p.at || Date.now()), tl: null };
  let scrub = null; // the time chosen with the mouse wheel on the timeline (played when the wheel stops)
  // the camera as a menu where the heading was (more compact than a button per camera); the same time, day and SD/HD
  const camPick = el('select', { class: 'chipSelect', 'aria-label': tr('Camera'), onchange: () => {
    if (camPick.value !== c.id) go('camera', { cam: camPick.value, at: st.at === null ? null : (A.at() || st.at), hd: st.hd, day: st.day, speed: st.speed }, true);
  } }, ...S.cams.map((k) => el('option', { value: k.id }, k.name)));
  camPick.value = c.id;
  camPick.hidden = S.cams.length < 2;
  const badge = el('span');
  const A = newPlayer(c, ctx);
  const sd = el('button', { type: 'button', class: 'small-btn' }, 'SD');
  const hd = el('button', { type: 'button', class: 'small-btn' }, 'HD');
  st.speed = p.speed || 1;
  const speedCtl = speedControl(A, st, ctx);
  A.buttons.append(sd, hd, speedCtl.el);
  // the camera's zones as outlines over the video (remembered per device)
  if ((c.zones || []).length) {
    const zoneLayer = el('div', { class: 'overlay', style: rot(c) + 'pointer-events:none' }, zoneSVG(c.zones, null));
    const zonesBtn = el('button', { type: 'button', class: 'small-btn', 'aria-pressed': 'false' }, tr('Zones'));
    const show = (on) => { zoneLayer.hidden = !on; zonesBtn.classList.toggle('selected', on); zonesBtn.setAttribute('aria-pressed', on ? 'true' : 'false'); save('showZones', on); };
    zonesBtn.onclick = () => show(zoneLayer.hidden);
    A.overlay.after(zoneLayer);
    show(!!load('showZones', false));
    A.buttons.append(zonesBtn);
  }
  // PTZ (cameras that can turn, found through ONVIF): a pad over the live video and the saved positions above the
  // timeline. A held button sends "move" every 400 ms; the server stops the camera by itself when that stops.
  const ptzSection = el('section', { class: 'tl ptzPositions', hidden: true });
  // a PC: the saved positions are buttons in the row with the camera menu and the days (the grid has no room for a section)
  const ptzInline = el('div', { class: 'chips ptzInline', hidden: true });
  const ptzLayer = el('div', { class: 'ptzLayer', hidden: true });
  A.box.append(ptzLayer);
  (async () => {
    let info;
    try { info = await api('/api/cams/' + c.id + '/ptz'); } catch (e) { return; }
    if (!info || !info.ptz || !root.isConnected) return;
    const flip = c.rotate === 180;
    let timer = null, cur = null;
    const send = (body) => api('/api/cams/' + c.id + '/ptz', { method: 'POST', body }).catch((e) => { A.notice(e.message); setTimeout(() => A.notice(''), 3000); });
    const stopMove = () => { if (!timer) return; clearInterval(timer); timer = null; send({ op: 'stop' }); };
    const startMove = (x, y, z) => {
      if (st.at !== null) { A.notice(tr('The camera can only be turned in the live view')); setTimeout(() => A.notice(''), 2500); return; }
      if (flip) { x = -x; y = -y; }
      cur = { op: 'move', x, y, z };
      send(cur);
      clearInterval(timer); timer = setInterval(() => send(cur), 400);
    };
    ctx.cleanup.push(stopMove);
    const arrow = (deg) => '<svg viewBox="0 0 24 24" style="transform:rotate(' + deg + 'deg)"><path d="M12 19V6M6.5 11.5L12 6l5.5 5.5"/></svg>';
    const padBtn = (x, y, z, label, html) => {
      const b = el('button', { type: 'button', class: 'ptzBtn', 'aria-label': label, html });
      b.addEventListener('pointerdown', (ev) => { ev.preventDefault(); ev.stopPropagation(); b.setPointerCapture(ev.pointerId); startMove(x, y, z); });
      for (const k of ['pointerup', 'pointercancel', 'lostpointercapture']) b.addEventListener(k, (ev) => { ev.stopPropagation(); stopMove(); });
      b.addEventListener('keydown', (ev) => { if ((ev.key === 'Enter' || ev.key === ' ') && !timer) { ev.preventDefault(); startMove(x, y, z); } });
      b.addEventListener('keyup', (ev) => { if (ev.key === 'Enter' || ev.key === ' ') stopMove(); });
      b.addEventListener('contextmenu', (ev) => ev.preventDefault());
      return b;
    };
    const v = 0.5; // speed
    const U = tr('Up'), D = tr('Down'), L = tr('Left'), R = tr('Right');
    // four directions: many cameras (e.g. Reolink E1) ignore turning and tilting at the same time
    const pad = el('div', { class: 'ptzPad' },
      el('span'), padBtn(0, v, 0, U, arrow(0)), el('span'),
      padBtn(-v, 0, 0, L, arrow(-90)), el('span'), padBtn(v, 0, 0, R, arrow(90)),
      el('span'), padBtn(0, -v, 0, D, arrow(180)), el('span'));
    ptzLayer.append(pad);
    if (info.zoom) ptzLayer.append(el('div', { class: 'ptzZoom' },
      padBtn(0, 0, v, tr('Zoom in'), '<svg viewBox="0 0 24 24"><path d="M12 6v12M6 12h12"/></svg>'),
      padBtn(0, 0, -v, tr('Zoom out'), '<svg viewBox="0 0 24 24"><path d="M6 12h12"/></svg>')));
    let lastPresets = [];
    const drawPresets = (presets) => {
      lastPresets = presets;
      const admin = S.user && S.user.admin;
      const chips = presets.map((pr) => {
        const goBtn = el('button', { type: 'button', class: 'chip', onclick: () => { if (st.at !== null) { st.at = null; play(); } send({ op: 'goto', preset: pr.token }); } }, pr.name);
        if (!admin) return goBtn;
        return el('span', { class: 'chipGroup' }, goBtn, el('button', { type: 'button', class: 'chip chipX', 'aria-label': tr('Delete {name}?', { name: pr.name }), onclick: async () => {
          if (!confirm(tr('Delete {name}?', { name: pr.name }))) return;
          try { const r = await api('/api/cams/' + c.id + '/ptz', { method: 'POST', body: { op: 'remove', preset: pr.token } }); drawPresets(r.presets || []); } catch (e) { alert(e.message); }
        } }, '×'));
      });
      const savePos = async () => {
        const name = (prompt(tr('Name of the position, e.g. Door')) || '').trim();
        if (!name) return;
        try { const r = await api('/api/cams/' + c.id + '/ptz', { method: 'POST', body: { op: 'save', name } }); drawPresets(r.presets || []); } catch (e) { alert(e.message); }
      };
      if (wide.matches) {
        ptzSection.replaceChildren();
        ptzInline.replaceChildren(...chips, admin ? el('button', { type: 'button', class: 'chip', title: tr('Save position'), onclick: savePos }, '+ ' + tr('Save position')) : null);
        if (!chips.length && !admin) ptzInline.append(el('span', { class: 'hint' }, tr('No saved positions yet – turn the camera and save the position.')));
        return;
      }
      ptzInline.replaceChildren();
      ptzSection.replaceChildren(el('div', { class: 'heading' }, el('h2', {}, tr('Positions'))),
        chips.length ? el('div', { class: 'chips' }, ...chips) : el('p', { class: 'hint' }, tr('No saved positions yet – turn the camera and save the position.')),
        admin ? el('div', { class: 'tl-buttons' }, el('button', { type: 'button', class: 'btn grey', onclick: savePos }, '+ ' + tr('Save position'))) : null);
    };
    const redrawPresets = () => drawPresets(lastPresets);
    wide.addEventListener('change', redrawPresets);
    ctx.cleanup.push(() => wide.removeEventListener('change', redrawPresets));
    drawPresets(info.presets || []);
    const ptzBtn = el('button', { type: 'button', class: 'small-btn', 'aria-pressed': 'false', title: tr('Turn the camera') }, 'PTZ');
    let ptzOn = false;
    const placePositions = () => { ptzSection.hidden = !ptzOn || wide.matches; ptzInline.hidden = !ptzOn || !wide.matches; };
    wide.addEventListener('change', placePositions);
    ctx.cleanup.push(() => wide.removeEventListener('change', placePositions));
    const showPTZ = (on) => { ptzOn = on; ptzLayer.hidden = !on; placePositions(); ptzBtn.classList.toggle('selected', on); ptzBtn.setAttribute('aria-pressed', on ? 'true' : 'false'); save('showPTZ', on); if (!on) stopMove(); };
    ptzBtn.onclick = () => showPTZ(ptzLayer.hidden);
    A.buttons.append(ptzBtn);
    showPTZ(load('showPTZ', true) !== false);
  })();
  const days = el('div', { class: 'chips' });
  const dateInput = el('input', { type: 'date', class: 'datePicker', 'aria-label': tr('Pick day') });
  const bar = el('div', { class: 'tl-bar', role: 'slider', 'aria-label': tr('Timeline – tap to seek'), tabindex: '0' });
  const head = el('div', { class: 'head' });
  const axisEl = el('div', { class: 'tl-axis' }, ...['00', '06', '12', '18', '24'].map((x) => el('span', {}, x)));
  const eventList = el('div', { style: 'display:flex;flex-direction:column;gap:6px' });
  // in the heading next to the days (not a row of its own): the timeline and the video stay where they are when it appears
  const toLive = el('button', { type: 'button', class: 'chip goLive', onclick: () => { st.at = null; play(); } }, tr('Go to live'));
  // a PC: the video fits its area, the timeline stays above the menu and the day's events are a column on the right
  // (CSS #screen.camScreen); a phone keeps the video at the top and scrolls the rest
  const camFit = el('div', { class: 'camFit' }, A.box);
  const wide = window.matchMedia('(min-width: 800px)');
  const ratio = (c.width || 4) / (c.height || 3);
  const fitVideo = () => {
    if (!wide.matches || document.body.classList.contains('full')) { A.box.style.width = ''; A.box.style.height = ''; return; }
    const W = camFit.clientWidth, H = camFit.clientHeight;
    if (!W || !H) return;
    const w = Math.min(W, H * ratio);
    A.box.style.width = Math.floor(w) + 'px'; A.box.style.height = Math.floor(w / ratio) + 'px';
  };
  const ro = new ResizeObserver(fitVideo);
  ro.observe(camFit);
  const redrawList = () => drawTimeline();
  wide.addEventListener('change', fitVideo);
  wide.addEventListener('change', redrawList);
  root.classList.add('camScreen');
  ctx.cleanup.push(() => { ro.disconnect(); wide.removeEventListener('change', fitVideo); wide.removeEventListener('change', redrawList); root.classList.remove('camScreen'); });
  root.append(topBar(c.name, { back: true, right: [badge] }), camFit,
    el('div', { class: 'page under-player' }, ptzSection,
      el('section', { class: 'tl' },
        el('div', { class: 'heading tlRow' }, camPick, ptzInline, el('div', { class: 'chips tlChoices' }, days, toLive)), axisEl, bar,
        el('p', { class: 'hint' }, MOUSE ? tr('Coloured = recorded · orange = event · click the line to seek') : tr('Coloured = recorded · orange = event · tap the line to seek')), dateInput),
      el('div', { class: 'content' }, el('h2', { class: 'small' }, tr('Events this day')), eventList)));

  function drawBadge() {
    const at = scrub !== null ? scrub : st.at;
    badge.replaceChildren(at === null ? el('span', { class: 'liveBadge' }, 'LIVE')
      : el('span', { class: 'timeBadge' }, (dayStart(at) === dayStart(Date.now()) ? '' : new Date(at).toLocaleDateString(LOC, { day: 'numeric', month: 'short' }) + ' ') + fmtTime(scrub !== null ? scrub : (A.at() || st.at), true)));
    sd.classList.toggle('selected', !st.hd); hd.classList.toggle('selected', st.hd);
    toLive.hidden = st.at === null;
    speedCtl.setLive(st.at === null);
  }
  async function play() {
    A.overlay.replaceChildren();
    if (st.at === null) {
      const base = '/api/live/' + c.id + '/' + (st.hd ? 'main' : 'sub');
      // the fast live view first (about half a second behind); HLS if the browser or the server cannot
      const fast = await A.playMSE(base + '/stream.mp4', () => { if (st.at === null) setTimeout(play, 1000); });
      if (!fast) await A.play(base + '/index.m3u8', true);
    } else {
      const from = Math.round(st.at - 60000), until = Math.round(st.at + 3 * 3600000);
      let ok = await A.play('/api/play/' + c.id + '/' + (st.hd ? 'main' : 'sub') + '?from=' + from + '&to=' + until, false, st.at);
      if (!ok && st.hd) { st.hd = false; A.notice(tr('Full resolution is not stored here – showing SD')); ok = await A.play('/api/play/' + c.id + '/sub?from=' + from + '&to=' + until, false, st.at); }
      speedCtl.apply();
    }
    drawBadge();
  }
  sd.onclick = () => { if (st.hd) { st.hd = false; st.at = A.at() && st.at !== null ? A.at() : st.at; play(); } };
  hd.onclick = () => { if (!st.hd) { st.hd = true; st.at = A.at() && st.at !== null ? A.at() : st.at; play(); } };

  function drawDays() {
    const i = dayStart(Date.now());
    const choices = [[tr('Today'), i], [tr('Yesterday'), i - DAY_MS]];
    const other = st.day !== i && st.day !== i - DAY_MS;
    days.replaceChildren(...choices.map(([n, d]) => el('button', { type: 'button', class: 'chip' + (st.day === d ? ' selected' : ''), onclick: () => { st.day = d; drawDays(); fetchTimeline(); } }, n)),
      el('button', { type: 'button', class: 'chip' + (other ? ' selected' : ''), onclick: () => { dateInput.showPicker ? dateInput.showPicker() : dateInput.click(); } },
        other ? new Date(st.day).toLocaleDateString(LOC, { day: 'numeric', month: 'short' }) : tr('Pick day')));
  }
  dateInput.max = new Date().toISOString().slice(0, 10);
  dateInput.onchange = () => { if (dateInput.value) { const [y, m, d] = dateInput.value.split('-').map(Number); st.day = new Date(y, m - 1, d).getTime(); drawDays(); fetchTimeline(); } };

  async function fetchTimeline() {
    const from = st.day, until = Math.min(st.day + DAY_MS, Date.now() + 60000);
    try { st.tl = await api('/api/timeline/' + c.id + '?from=' + from + '&to=' + until); } catch (e) { st.tl = null; }
    drawTimeline();
  }
  const pct = (t) => Math.max(0, Math.min(100, (t - st.day) / DAY_MS * 100));
  function drawTimeline() {
    const items = [];
    const tl = st.tl || { sub: [], main: [], events: [] };
    for (const [a, b] of tl.sub) items.push(el('div', { class: 'opt', style: `left:${pct(a)}%;width:${Math.max(0.2, pct(b) - pct(a))}%` }));
    for (const [a, b] of tl.main) items.push(el('div', { class: 'opt hd', style: `left:${pct(a)}%;width:${Math.max(0.2, pct(b) - pct(a))}%` }));
    for (const e of tl.events) items.push(el('button', { type: 'button', class: 'hm', style: `left:${pct(eventStart(e))}%`, 'aria-label': tr('Event') + ' ' + fmtTime(eventStart(e)) + ' ' + eventLabel(e),
      onclick: (ev) => { ev.stopPropagation(); go('playback', { id: e.id, list: tl.events.map((x) => x.id), ev: e }); } }));
    items.push(head);
    bar.replaceChildren(...items);
    const evs = tl.events.slice().reverse ? tl.events.slice() : [];
    // a PC: rows with a picture in the column on the right; a phone: the short list under the timeline (less data)
    eventList.replaceChildren(...(wide.matches ? dayEventRows(evs) : evs.length ? evs.map((e) => el('button', { type: 'button', class: 'list-row', onclick: () => go('playback', { id: e.id, list: evs.map((x) => x.id), ev: e }) },
      el('span', {}, fmtTime(eventStart(e), true)), el('span', { class: 'l' }, eventLabel(e)))) : [el('p', { class: 'hint' }, tr('No events'))]));
    moveHead();
  }
  function moveHead() {
    const t = scrub !== null ? scrub : st.at === null ? Date.now() : (A.at() || st.at);
    head.style.left = pct(t) + '%';
    head.hidden = t < st.day || t > st.day + DAY_MS;
  }
  bar.addEventListener('click', (ev) => {
    const r = bar.getBoundingClientRect();
    const t = Math.round(st.day + (ev.clientX - r.left) / r.width * DAY_MS);
    if (t > Date.now() - 30000) { st.at = null; } else st.at = t;
    play();
  });
  // the mouse wheel on the line: 1 minute per step (up = later, down = earlier); the video is loaded once the wheel stops
  const wheelStep = wheelStepper();
  let scrubTimer = null;
  bar.title = tr('Mouse: scroll on the video to zoom · scroll on the line to jump 1 minute');
  A.box.title = bar.title; // the same help over the video (a PC shows it when the mouse rests there)
  bar.addEventListener('wheel', (ev) => {
    ev.preventDefault();
    const n = -wheelStep(ev);
    if (!n) return;
    const base = scrub !== null ? scrub : st.at === null ? Date.now() : (A.at() || st.at);
    scrub = Math.max(st.day, Math.min(base + n * 60000, st.day + DAY_MS - 1, Date.now()));
    moveHead(); drawBadge();
    clearTimeout(scrubTimer);
    scrubTimer = setTimeout(() => { const t = scrub; scrub = null; st.at = t > Date.now() - 30000 ? null : t; play(); }, 400);
  }, { passive: false });
  ctx.cleanup.push(() => clearTimeout(scrubTimer));

  // live boxes from the server
  interval(ctx, async () => {
    if (st.at === null && S.opt.liveBoxes) {
      await fetchCameras();
      A.drawBoxes(camById(c.id).boxes);
    } else A.overlay.replaceChildren();
    moveHead();
    if (st.at !== null) drawBadge();
  }, 1000);
  interval(ctx, () => { if (st.day === dayStart(Date.now())) fetchTimeline(); }, 60000);
  drawDays(); drawBadge(); play();
};

SCREENS.playback = async (root, p, ctx) => {
  let e = p.ev || S.events.find((x) => x.id === p.id);
  if (!e) {
    try { const l = await api('/api/events?limit=500'); e = l.find((x) => x.id === p.id); } catch (err2) { /* */ }
    if (!e) { root.append(topBar(tr('Event'), { back: true }), el('p', { class: 'empty' }, tr('The event no longer exists'))); return; }
  }
  if (e.id > S.seen) { S.seen = Math.max(S.seen, e.id); }
  const c = camById(e.cam);
  const start = eventStart(e), endT = eventEnd(e);
  const preS = c.pre_seconds !== undefined ? c.pre_seconds : 10, postS = c.post_seconds !== undefined ? c.post_seconds : 20;
  const from = start - preS * 1000, until = endT + postS * 1000;
  // on mobile data events start in SD (one HD minute can be 20+ MB) – the HD button switches; the option is in General
  const st = { hd: !(S.opt.mobileSD && onMobileData()), speed: 1 };
  const A = newPlayer(c, ctx);
  // poster with boxes until the video plays
  const posterImg = el('img', { alt: '' });
  const poster = el('div', { class: 'poster' }, withBoxes(posterImg, c, e.boxes, true));
  loadImage(posterImg, '/api/events/' + e.id + '/thumb.jpg', true);
  A.overlay.parentNode.insertBefore(poster, A.overlay);
  A.video.addEventListener('playing', () => { poster.hidden = true; });
  const sd = el('button', { type: 'button', class: 'small-btn' }, 'SD');
  const hdBtn = el('button', { type: 'button', class: 'small-btn' }, 'HD');
  A.buttons.append(sd, hdBtn);
  A.clock.hidden = false;

  const list = p.list || [];
  const idx = list.indexOf(e.id);
  const neighbours = (d) => { const n = list[idx + d]; if (n !== undefined) go('playback', { id: n, list }, true); };
  const playBtn = el('button', { type: 'button', class: 'play filled', 'aria-label': tr('Pause'), html: ICON.pause });
  const head = el('div', { class: 'head' });
  const bar = el('div', { class: 'tl-bar', style: 'height:34px' },
    el('div', { class: 'hm shown', style: `left:${(start - from) / (until - from) * 100}%;width:${(endT - start) / (until - from) * 100}%` }), head);
  // the video fills what is left of the screen (in its own shape); the controls and the timeline stay fixed above the menu
  const fit = el('div', { class: 'fitPlayer' }, A.box);
  const ratio = (c.width || 4) / (c.height || 3);
  const resize = () => {
    if (document.body.classList.contains('full')) return;
    const W = fit.clientWidth, H = fit.clientHeight;
    if (!W || !H) return;
    const w = Math.min(W, H * ratio);
    A.box.style.width = Math.floor(w) + 'px'; A.box.style.height = Math.floor(w / ratio) + 'px';
  };
  const ro = new ResizeObserver(resize);
  ro.observe(fit);
  // a PC: the camera's events that day in a column on the right (as on the timeline), this one marked – fetched only there
  const wideP = window.matchMedia('(min-width: 800px)');
  const sideList = el('div', { style: 'display:flex;flex-direction:column;gap:6px' });
  const side = el('div', { class: 'content evSide under-player' }, el('h2', { class: 'small' }, tr('Events this day')), sideList);
  let sideDone = false;
  const fillSide = async () => {
    if (!wideP.matches || sideDone) return;
    sideDone = true;
    const day = dayStart(start);
    let evs = [];
    try { evs = (await api('/api/timeline/' + c.id + '?from=' + day + '&to=' + Math.min(day + DAY_MS, Date.now() + 60000))).events || []; } catch (err) { sideDone = false; return; }
    sideList.replaceChildren(...dayEventRows(evs, e.id));
    const cur = sideList.querySelector('.current');
    if (cur) cur.scrollIntoView({ block: 'center' });
  };
  wideP.addEventListener('change', fillSide);
  // the time being played, as on the timeline (top right)
  const badge = el('span');
  const drawBadge = () => {
    const t = A.at() || start;
    badge.replaceChildren(el('span', { class: 'timeBadge' }, (dayStart(t) === dayStart(Date.now()) ? '' : new Date(t).toLocaleDateString(LOC, { day: 'numeric', month: 'short' }) + ' ') + fmtTime(t, true)));
  };
  drawBadge();
  const pc = wideP.matches;
  // switching between a PC and a phone layout (a window made narrow): draw the screen again
  const relayout = () => { if (wideP.matches !== pc) go('playback', p, true); };
  wideP.addEventListener('change', relayout);
  root.classList.add(pc ? 'playPC' : 'playScreen');
  ctx.cleanup.push(() => { ro.disconnect(); wideP.removeEventListener('change', fillSide); wideP.removeEventListener('change', relayout); root.classList.remove('playScreen', 'playPC'); });
  if (pc) {
    // A PC: built like the timeline, so it feels like the same player – the video keeps its size and place, and
    // under it the same rows: buttons, a heading with choices, the bar (here the event instead of the whole day), a hint.
    const status = el('span', { role: 'status', class: 'pbStatus' });
    const say = (t) => { status.textContent = t ? ' · ' + t : ''; };
    const chipBtn = (icon, label, onclick, extra) => el('button', Object.assign({ type: 'button', class: 'chip iconChip', html: icon, 'aria-label': label, title: label, onclick }, extra || {}));
    playBtn.className = 'chip iconChip filled selected';
    const protect = chipBtn(ICON.flag, tr('Protect'), async () => {
      try { await protectEvent(e); drawProtect(); say(e.keep ? tr('The event and the recording around it will not be deleted.') : tr('Protection removed – deleted after 30 days as usual.')); } catch (err) { say(err.message); }
    });
    const drawProtect = () => { protect.classList.toggle('selected', !!e.keep); protect.setAttribute('aria-pressed', e.keep ? 'true' : 'false');
      protect.title = tr(e.keep ? 'Protected' : 'Protect'); protect.disabled = !!e.keep && !canUnprotect(); };
    drawProtect();
    const actions = [protect, chipBtn(ICON.download, tr('Download video'), () => downloadEvent(e, say))];
    if (S.user && S.user.admin) actions.push(chipBtn(ICON.trash, tr('Delete'), async () => { try { if (await deleteEvent(e)) back(); } catch (err) { say(err.message); } }));
    bar.style.height = '';
    root.append(topBar(c.name, { back: true, right: [badge] }), fit,
      el('section', { class: 'tl under-player pbPanel' },
        // one row, like the camera menu and the days on the timeline: play buttons and what was found | protect,
        // download, delete, live, timeline (the date and length are in the badge at the top and the marked event on the right)
        el('div', { class: 'heading tlRow' },
          el('div', { class: 'chips pbRow' },
            chipBtn(ICON.back10, tr('Back 10 seconds'), () => { A.video.currentTime = Math.max(0, A.video.currentTime - 10); }),
            chipBtn(ICON.previous, tr('Newer event'), () => neighbours(-1), { class: 'chip iconChip filled', disabled: idx <= 0 }),
            playBtn,
            chipBtn(ICON.next, tr('Older event'), () => neighbours(1), { class: 'chip iconChip filled', disabled: idx < 0 || idx >= list.length - 1 }),
            el('span', { class: 'eventLabels pbLabels', 'aria-label': tr('Detected') }, ...(e.labels && e.labels.length ? e.labels.map((l) => el('span', { class: 'eventLabel' }, labelName(l))) : [el('span', { class: 'eventLabel' }, tr('Motion'))]),
              e.score ? el('span', { class: 'eventLabel' }, tr('{p} % certain', { p: Math.round(e.score * 100) })) : null)),
          el('div', { class: 'chips tlChoices' }, ...actions,
            el('button', { type: 'button', class: 'chip', onclick: () => go('camera', { cam: c.id }) }, tr('Live')),
            el('button', { type: 'button', class: 'chip', onclick: () => go('camera', { cam: c.id, at: start }) }, tr('Timeline')))),
        el('div', { class: 'tl-axis' }, el('span', {}, fmtTime(from, true)), el('span', {}, fmtTime(until, true))), bar,
        el('p', { class: 'hint' }, tr(MOUSE ? 'Orange = the event · {f} s before and {e} s after are included · click to seek' : 'Orange = the event · {f} s before and {e} s after are included · tap to seek', { f: preS, e: postS }), status)), side);
    fillSide();
  } else {
    root.append(topBar(c.name, { back: true, under: dayName(start) + ' ' + fmtTime(start, true) + ' · ' + duration(endT - start) }), fit,
      el('div', { class: 'controls under-player' },
        el('button', { type: 'button', 'aria-label': tr('Back 10 seconds'), html: ICON.back10, onclick: () => { A.video.currentTime = Math.max(0, A.video.currentTime - 10); } }),
        el('button', { type: 'button', class: 'filled', 'aria-label': tr('Newer event'), html: ICON.previous, disabled: idx <= 0, style: idx <= 0 ? 'opacity:.35' : '', onclick: () => neighbours(-1) }),
        playBtn,
        el('button', { type: 'button', class: 'filled', 'aria-label': tr('Older event'), html: ICON.next, disabled: idx < 0 || idx >= list.length - 1, style: idx < 0 || idx >= list.length - 1 ? 'opacity:.35' : '', onclick: () => neighbours(1) })),
      el('div', { class: 'tl under-player fixed', style: 'background:none' },
        el('div', { class: 'tl-axis' }, el('span', {}, fmtTime(from, true)), el('span', {}, fmtTime(until, true))), bar,
        el('p', { class: 'hint' }, tr(MOUSE ? 'Orange = the event · {f} s before and {e} s after are included · click to seek' : 'Orange = the event · {f} s before and {e} s after are included · tap to seek', { f: preS, e: postS }))),
      el('div', { class: 'under-player details' },
        el('div', { class: 'content' },
          el('h2', { class: 'small' }, tr('Detected')),
          el('div', { class: 'eventLabels' }, ...(e.labels && e.labels.length ? e.labels.map((l) => el('span', { class: 'eventLabel' }, labelName(l))) : [el('span', { class: 'eventLabel' }, tr('Motion'))]),
            e.score ? el('span', { class: 'eventLabel' }, tr('{p} % certain', { p: Math.round(e.score * 100) })) : null),
          eventActions(e),
          el('div', { class: 'to' },
            el('button', { type: 'button', class: 'btn grey', html: ICON.camera + tr('Watch live'), onclick: () => go('camera', { cam: c.id }) }),
            el('button', { type: 'button', class: 'btn grey', html: ICON.clock + tr('Open timeline'), onclick: () => go('camera', { cam: c.id, at: start }) })))), side);
    fillSide();
  }

  playBtn.onclick = () => { if (A.video.paused) A.video.play().catch(() => {}); else A.video.pause(); };
  A.video.addEventListener('play', () => { playBtn.innerHTML = ICON.pause; playBtn.setAttribute('aria-label', tr('Pause')); });
  A.video.addEventListener('pause', () => { playBtn.innerHTML = ICON.play; playBtn.setAttribute('aria-label', tr('Play')); });
  const speedCtl = speedControl(A, st, ctx);
  A.buttons.append(speedCtl.el);
  bar.addEventListener('click', (ev) => {
    const r = bar.getBoundingClientRect();
    const t = from + (ev.clientX - r.left) / r.width * (until - from);
    const nowT = A.at();
    if (nowT) A.video.currentTime = Math.max(0, A.video.currentTime + (t - nowT) / 1000);
  });
  // the mouse wheel on the bar: 10 seconds per step (up = later, down = earlier)
  const wheelStep = wheelStepper();
  bar.title = tr('Mouse: scroll on the video to zoom · scroll on the line to jump 10 seconds');
  A.box.title = bar.title; // the same help over the video (a PC shows it when the mouse rests there)
  bar.addEventListener('wheel', (ev) => {
    ev.preventDefault();
    const n = -wheelStep(ev), t = A.at();
    if (!n || !t) return;
    const want = Math.max(from, Math.min(until, t + n * 10000));
    A.video.currentTime = Math.max(0, A.video.currentTime + (want - t) / 1000);
  }, { passive: false });
  A.video.addEventListener('timeupdate', () => {
    const t = A.at();
    if (!t) return;
    A.clock.textContent = fmtTime(t, true);
    drawBadge();
    head.style.left = Math.max(0, Math.min(100, (t - from) / (until - from) * 100)) + '%';
    if (t > until + 1000 && !A.video.paused) A.video.pause();
  });
  async function play() {
    sd.classList.toggle('selected', !st.hd); hdBtn.classList.toggle('selected', st.hd);
    const t = A.at() || from;
    let ok = await A.play('/api/play/' + c.id + '/' + (st.hd ? 'main' : 'sub') + '?from=' + from + '&to=' + until, false, t);
    if (!ok && st.hd) { st.hd = false; sd.classList.add('selected'); hdBtn.classList.remove('selected'); ok = await A.play('/api/play/' + c.id + '/sub?from=' + from + '&to=' + until, false, t); }
    if (!ok) A.notice(tr('The recording is no longer stored'));
    speedCtl.apply();
  }
  sd.onclick = () => { if (st.hd) { st.hd = false; play(); } };
  hdBtn.onclick = () => { if (!st.hd) { st.hd = true; play(); } };
  play();
};

// Download / protect / delete an event
// downloadEvent: the event's clip as MP4 (the phone: Android's downloads; the browser: a file). say(text) shows progress.
async function downloadEvent(e, say) {
  const t = new Date(eventStart(e));
  const name = 'ArgusSen_' + camName(e.cam).replace(/\s+/g, '_') + '_' + t.getFullYear() + '-' + to(t.getMonth() + 1) + '-' + to(t.getDate()) + '_' + to(t.getHours()) + '-' + to(t.getMinutes()) + '-' + to(t.getSeconds()) + '.mp4';
  const url = S.server + '/api/events/' + e.id + '/clip.mp4';
  if (NATIVE && NATIVE.download) {
    say(NATIVE.download(url, name) ? tr('Downloading … the file goes to Downloads (see the notification).') : tr('Could not start the download.'));
    return;
  }
  say(tr('Downloading …'));
  try {
    const b = await api('/api/events/' + e.id + '/clip.mp4', { blob: true });
    const a = el('a', { href: URL.createObjectURL(b), download: name }); document.body.append(a); a.click(); a.remove();
    say(tr('Downloaded.'));
  } catch (err) { say(err.message); }
}
// protectEvent: protect the event (or remove the protection – administrators only). Returns the new state.
async function protectEvent(e) {
  const r = await api('/api/events/' + e.id + '/keep', { method: 'POST', body: { keep: !e.keep } });
  e.keep = r.keep;
  const x = S.events.find((y) => y.id === e.id); if (x) x.keep = e.keep;
  return e.keep;
}
// deleteEvent: asks first; true when it was deleted (administrators only).
async function deleteEvent(e) {
  if (!confirm(tr('Delete the event? This cannot be undone.'))) return false;
  await api('/api/events/' + e.id, { method: 'DELETE' });
  S.events = S.events.filter((y) => y.id !== e.id);
  return true;
}
const canUnprotect = () => !!(S.user && S.user.admin);

function eventActions(e) {
  const status = el('p', { class: 'hint', role: 'status' });
  const say = (t) => { status.textContent = t; };
  const protectBtn = el('button', { type: 'button', class: 'btn grey' });
  // everybody may protect an event; only administrators may remove the protection
  const drawProtect = () => { protectBtn.innerHTML = ICON.flag + tr(e.keep ? 'Protected' : 'Protect'); protectBtn.classList.toggle('active', !!e.keep); protectBtn.setAttribute('aria-pressed', e.keep ? 'true' : 'false');
    protectBtn.disabled = !!e.keep && !canUnprotect(); };
  drawProtect();
  protectBtn.onclick = async () => {
    try {
      await protectEvent(e); drawProtect();
      say(e.keep ? tr('The event and the recording around it will not be deleted.') : tr('Protection removed – deleted after 30 days as usual.'));
    } catch (err) { say(err.message); }
  };
  const load = el('button', { type: 'button', class: 'btn grey', html: ICON.download + tr('Download video'), onclick: () => downloadEvent(e, say) });
  const items = [load, protectBtn];
  if (S.user && S.user.admin) items.push(el('button', { type: 'button', class: 'btn grey', html: ICON.trash + tr('Delete'), onclick: async () => {
    try { if (await deleteEvent(e)) back(); } catch (err) { say(err.message); }
  } }));
  return el('div', { style: 'display:flex;flex-direction:column;gap:6px' }, el('div', { class: 'three' }, ...items), status);
}

// eventIcons: protect, download and delete as small icons next to an event in the list (onGone: it was deleted)
function eventIcons(e, onGone) {
  const say = (t) => { if (t && !NATIVE) btnDl.title = t; };
  const flag = el('button', { type: 'button', class: 'evIcon', html: ICON.flag });
  const drawFlag = () => {
    flag.classList.toggle('on', !!e.keep);
    flag.setAttribute('aria-pressed', e.keep ? 'true' : 'false');
    flag.setAttribute('aria-label', e.keep ? tr('Protected') : tr('Protect'));
    flag.title = e.keep ? tr('Protected') : tr('Protect');
    flag.disabled = !!e.keep && !canUnprotect();
  };
  drawFlag();
  flag.onclick = async (ev) => { ev.stopPropagation(); try { await protectEvent(e); drawFlag(); } catch (err) { alert(err.message); } };
  const btnDl = el('button', { type: 'button', class: 'evIcon', html: ICON.download, 'aria-label': tr('Download video'), title: tr('Download video'),
    onclick: (ev) => { ev.stopPropagation(); downloadEvent(e, say); } });
  const items = [flag, btnDl];
  if (S.user && S.user.admin) items.push(el('button', { type: 'button', class: 'evIcon', html: ICON.trash, 'aria-label': tr('Delete'), title: tr('Delete'),
    onclick: async (ev) => { ev.stopPropagation(); try { if (await deleteEvent(e)) onGone(); } catch (err) { alert(err.message); } } }));
  return el('div', { class: 'eventIcons' }, ...items);
}

SCREENS.status = (root, p, ctx) => {
  const admin = S.user && S.user.admin;
  const content = el('div', { class: 'content' });
  root.append(topBar(tr('Status')), el('div', { class: 'page' }, content));
  const ring = (label, v, colour) => el('div', { class: 'ring' },
    el('div', { class: 'r', style: `background:conic-gradient(${colour} 0 ${v}%, #454545 ${v}% 100%)` }, el('span', {}, Math.round(v) + ' %')), label);
  interval(ctx, async () => {
    let d = null;
    try { d = await api('/api/status'); } catch (e) { /* an older ArgusSen: only administrators – the camera list below */ }
    const cams = await fetchCameras();
    const sys = d && d.system;
    const ver = NATIVE ? NATIVE.versionName() : 'web';
    const items = [el('div', { class: 'logo-large', html: ICON.eyeColour + '<b>ARGUSSEN</b>' },
      el('span', { class: 'hint' }, 'App ' + ver + (d ? ' · server ' + d.version + ' · ' + tr('up') + ' ' + uptime(sys.uptime_s) : '')))];
    if (sys && sys.storage_ok === false) items.push(el('p', { class: 'errorText' }, tr('The storage (network drive) is not available – recording is paused.')));
    if (sys && sys.storage_low) items.push(el('p', { class: 'errorText' }, tr('The storage is almost full and only recordings around protected events are left. Protect fewer events, keep recordings for a shorter time or free space.')));
    if (sys && sys.ai_note === 'experimental') items.push(el('p', { class: 'hint do' }, tr('The built-in AI is experimental on this machine (ARM, e.g. Raspberry Pi) and may be slow.')));
    if (sys && sys.storage_local) items.push(el('p', { class: 'errorText' }, tr('The NAS is not responding – recording to the local disk ({gb} GB free). Recordings are moved to the NAS when it is back.', { gb: Math.round(sys.nas.free_gb || 0) })));
    if (sys) {
      const own = sys.argussen || {};
      // with a limit, the ring shows ArgusSen's use of it; otherwise how full the disk/NAS is
      const storageRing = own.max_gb > 0 && own.used_gb >= 0
        ? ring('ArgusSen', Math.min(100, 100 * own.used_gb / own.max_gb), '#5CC8BB')
        : ring(S.ha || S.standalone ? tr('Storage') : 'NAS', sys.nas.used_pct || 0, '#5CC8BB');
      items.push(el('div', { class: 'rings' }, ring('CPU', sys.cpu_pct, '#3DBE6B'), ring('RAM', sys.mem_pct, '#3DBE6B'), storageRing));
      // as a Home Assistant app: the rings show the app's own use (not the whole machine)
      if (sys.app) items.push(el('p', { class: 'hint', style: 'text-align:center' }, tr('The app uses: CPU {cpu} % · RAM {mem} of {limit}', {
        cpu: (Math.round(sys.cpu_pct * 10) / 10).toString().replace('.', ','),
        mem: sys.app.mem_mb >= 1000 ? (sys.app.mem_mb / 1000).toFixed(2).replace('.', ',') + ' GB' : sys.app.mem_mb + ' MB',
        limit: (sys.app.mem_limit_mb / 1000).toFixed(2).replace('.', ',') + ' GB' })));
    }
    // the home mode tells whether the family is home: administrators only (the server refuses the others)
    if (admin) try {
      const m = await api('/api/mode');
      const names = m.events_off.map(camName).join(', ');
      items.push(el('div', { class: 'camRow' }, el('span', { class: 'dot', style: 'background:' + (m.home ? '#F59A3C' : '#5CC8BB') }),
        el('b', {}, m.home ? tr('Home') : tr('Away')), el('span', {}, m.home ? (names ? tr('no events: {names}', { names: names }) : tr('all cameras active')) : tr('all cameras active'))));
    } catch (e) { /* */ }
    items.push(el('h2', { class: 'small' }, tr('Cameras')));
    const source = (d && d.cameras) || cams || [];
    for (const c of source) {
      const on = d ? c.sub_ok : c.online;
      const info = d ? (c.sub_ok && c.main_ok ? tr('recording') : c.sub_ok ? tr('SD only – {f}', { f: c.main_error ? tr(c.main_error) : tr('HD missing') }) : (c.sub_error || tr('no signal'))) : (on ? tr('online') : tr('no signal'));
      items.push(el('div', { class: 'camRow' }, el('span', { class: 'dot' + (on ? '' : ' off') }), el('b', {}, c.name), el('span', {}, info)));
    }
    if (sys) {
      const own = sys.argussen || {};
      const size = (x) => x >= 1000 ? (x / 1000).toFixed(1).replace('.', ',') + ' TB' : Math.round(x) + ' GB';
      items.push(el('div', { class: 'stats' },
        el('div', {}, tr('Events today'), el('b', {}, sys.events_today)),
        own.used_gb >= 0 ? el('div', {}, tr('ArgusSen uses'), el('b', {}, own.max_gb > 0 ? size(own.used_gb) + ' / ' + size(own.max_gb) : size(own.used_gb))) : null,
        el('div', {}, S.ha || S.standalone ? tr('Free storage') : tr('Free on NAS'),
          el('b', {}, size(sys.nas.free_gb || 0) + (sys.nas.total_gb ? ' / ' + size(sys.nas.total_gb) : '')))));
    }
    content.replaceChildren(...items);
  }, 5000);
};
SCREENS.log = (root, p, ctx) => {
  const KIND_COLOURS = { error: '#D63A2C', ok: '#3DBE6B', alarm: '#F59A3C', user: '#C8C8C8', info: '#5CC8BB' };
  const filter = el('input', { type: 'search', placeholder: tr('Filter (camera, text, error …)'), 'aria-label': tr('Filter') });
  const list = el('div');
  const moreBtn = el('button', { type: 'button', class: 'moreBtn', hidden: true }, tr('Load older'));
  root.append(topBar(tr('Log'), { back: true }), el('div', { class: 'filters' }, el('label', { class: 'search', html: ICON.search }, filter)),
    el('div', { class: 'page' }, list, moreBtn));
  let lines = [];
  const row = (l) => el('div', { class: 'logRow' },
    el('span', { class: 'dot', style: 'background:' + (KIND_COLOURS[l.kind] || KIND_COLOURS.info) }),
    el('div', { class: 'logText' },
      el('div', { class: 'logHead' }, el('b', {}, camName(l.obj)), el('span', {}, dayName(new Date(l.time).getTime()) + ' ' + fmtTime(new Date(l.time).getTime(), true))),
      el('div', { class: 'logMsg' }, l.msg)));
  async function fetchLog(more) {
    const q = encodeURIComponent(filter.value.trim());
    const from = more && lines.length ? '&before=' + lines[lines.length - 1].id : '';
    try {
      const newItems = await api('/api/log?limit=200&q=' + q + from);
      lines = more ? lines.concat(newItems) : newItems;
      list.replaceChildren(...(lines.length ? lines.map(row) : [el('p', { class: 'empty' }, tr('No lines'))]));
      moreBtn.hidden = newItems.length < 200;
    } catch (e) { list.replaceChildren(el('p', { class: 'errorText' }, e.message)); }
  }
  let t = null;
  filter.oninput = () => { clearTimeout(t); t = setTimeout(() => fetchLog(false), 300); };
  moreBtn.onclick = () => fetchLog(true);
  interval(ctx, () => { if (!filter.value) fetchLog(false); }, 10000);
};
function uptime(s) { const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60); return d ? tr('{d} d {h} h', { d, h }) : h ? tr('{h} h {m} m', { h, m }) : tr('{m} min', { m }); }

// Empty slots (null/false) in element lists are skipped instead of being shown as text.
for (const f of ['append', 'prepend', 'replaceChildren']) {
  const o = Element.prototype[f];
  Element.prototype[f] = function (...xs) { return o.apply(this, xs.filter((x) => x !== null && x !== undefined && x !== false)); };
}
// ---------- settings: menu with Users, Cameras, General and Log ----------
const MENU_ICON = {
  zones: '<svg viewBox="0 0 24 24"><path d="M3 7l7-3 9 4-2 11-11 1z"/><circle cx="3" cy="7" r="1.5" fill="currentColor"/><circle cx="10" cy="4" r="1.5" fill="currentColor"/><circle cx="19" cy="8" r="1.5" fill="currentColor"/><circle cx="17" cy="19" r="1.5" fill="currentColor"/><circle cx="6" cy="20" r="1.5" fill="currentColor"/></svg>',
  userList: '<svg viewBox="0 0 24 24"><circle cx="9" cy="8" r="4"/><path d="M2 21c0-4 3-7 7-7s7 3 7 7"/><path d="M16 4a4 4 0 0 1 0 8M22 21c0-3-2-6-5-6.5"/></svg>',
  cameras: '<svg viewBox="0 0 24 24"><rect x="2" y="6" width="14" height="12" rx="2"/><path d="M16 10l6-3v10l-6-3z"/></svg>',
  general: '<svg viewBox="0 0 24 24"><path d="M4 6h16M4 12h16M4 18h16"/><circle cx="9" cy="6" r="2" fill="currentColor"/><circle cx="15" cy="12" r="2" fill="currentColor"/><circle cx="7" cy="18" r="2" fill="currentColor"/></svg>',
  log: '<svg viewBox="0 0 24 24"><path d="M6 3h9l4 4v14H6z"/><path d="M9 11h7M9 15h7M9 7h4"/></svg>',
  storage: '<svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><path d="M7 7.5h.01M7 16.5h.01"/></svg>',
  network: '<svg viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="8" rx="1.5"/><path d="M7 7h.01M12 11v5M5 20h14M12 16a2 2 0 1 0 0 4 2 2 0 0 0 0-4z"/></svg>',
  ha: '<svg viewBox="0 0 24 24"><path d="M3 11l9-7 9 7"/><path d="M5 10v10h14V10"/><circle cx="12" cy="14" r="2"/><path d="M12 16v4M12 12V9"/></svg>',
  ai: '<svg viewBox="0 0 24 24"><path d="M2 12c3-5 6.5-7 10-7s7 2 10 7c-3 5-6.5 7-10 7s-7-2-10-7z"/><circle cx="12" cy="12" r="3"/><path d="M19 3l.6 1.4L21 5l-1.4.6L19 7l-.6-1.4L17 5l1.4-.6z"/></svg>',
  arrow: '<svg viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/></svg>',
  plus: '<svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>',
};

function menuRow(iconHtml, heading2, under, onclick) {
  return el('button', { type: 'button', class: 'menuRow', onclick },
    el('span', { class: 'mi', html: iconHtml }),
    el('span', { class: 'mt' }, el('b', {}, heading2), under ? el('small', {}, under) : null),
    el('span', { class: 'mp', html: MENU_ICON.arrow }));
}

function toggleButton(heading2, hint, until, onChange) {
  const b = el('button', { type: 'button', class: 'toggle', role: 'switch', 'aria-checked': until ? 'true' : 'false',
    onclick: () => { until = !until; b.setAttribute('aria-checked', until ? 'true' : 'false'); onChange(until); } },
  el('span', { class: 't' }, el('b', {}, heading2), hint ? el('small', {}, hint) : null), el('span', { class: 'track' }));
  return b;
}

// field: a label with an input and an optional help text (important = something to do or be aware of: bold turquoise)
function field(label, input, hint, important) {
  return el('label', { class: 'field' }, label, input, hint ? el('small', { class: important ? 'hint do' : 'hint' }, hint) : null);
}

SCREENS.settings = (root) => {
  const admin = S.user && S.user.admin;
  const content = el('div', { class: 'content' });
  root.append(topBar(tr('Settings')), el('div', { class: 'page' }, content));
  if (admin) {
    content.append(
      menuRow(MENU_ICON.userList, tr('Users'), tr('Create users, camera access, passwords'), () => go('set_users')),
      menuRow(MENU_ICON.cameras, tr('Cameras'), tr('Add and edit cameras, motion and AI'), () => go('set_cameras')));
    if (S.ha || S.standalone) content.append(menuRow(MENU_ICON.storage, tr('Storage'), tr('Where recordings are stored'), () => go('set_storage')));
  }
  content.append(menuRow(MENU_ICON.general, tr('General'), tr('Display, my account, app version'), () => go('set_general')));
  if (admin) content.append(menuRow(MENU_ICON.log, tr('Log'), tr('Everything ArgusSen has done'), () => go('log')));
};

// ---------- storage (Home Assistant version only) ----------
// How long recordings are kept, and the space it will take (from the cameras' real file sizes).
function retentionBox(d, gb) {
  const rt = d.retention || { sub_days: 14, main_hours: 24, event_days: 30 };
  const sub = camInput(rt.sub_days, { inputmode: 'numeric' });
  const main = camInput(rt.main_hours, { inputmode: 'numeric' });
  const evd = camInput(rt.event_days, { inputmode: 'numeric' });
  const cur = (d.places || []).find((p) => p.current);
  const est = el('p', { class: 'hint' });
  let maxGB = d.max_gb || 0;
  const showEst = (e) => {
    est.className = 'hint';
    est.textContent = tr('Expected space: about {gb} for the 24/7 recordings (plus HD around events).', { gb: gb(e) });
    // the place must also keep its minimum free space (Home Assistant's own disk: 10 %, at least 10 GB) – and the limit
    if ((cur && d.used_gb >= 0 && e > cur.free_gb + d.used_gb - (d.min_free_gb || 0)) || (maxGB > 0 && e > maxGB)) {
      est.className = 'errorText';
      est.textContent += ' ' + tr('That is more than there is room for – the oldest recordings will be deleted before they expire.');
    }
  };
  showEst(d.estimate_gb || 0);
  const status = el('p', { class: 'hint', role: 'status' });
  // the default follows the storage (Home Assistant's own disk below 128 GB: SD 3 days, HD only around events)
  const dr = d.default_retention;
  const defText = el('p', { class: 'hint' });
  const resetBtn = el('button', { type: 'button', class: 'btn grey' }, tr('Use the default'));
  const showDefault = (isDefault) => {
    defText.textContent = dr ? (isDefault ? tr('Default for this storage') + ': ' : tr('The default for this storage would be') + ': ')
      + tr('SD {sub} days, HD {main} hours, events {ev} days', { sub: dr.sub_days, main: dr.main_hours, ev: dr.event_days }) : '';
    resetBtn.hidden = isDefault;
  };
  showDefault(d.retention_default !== false);
  resetBtn.onclick = async () => {
    try {
      const r = await api('/api/admin/retention', { method: 'PUT', body: { default: true } });
      sub.value = r.retention.sub_days; main.value = r.retention.main_hours; evd.value = r.retention.event_days;
      showEst(r.estimate_gb || 0); showDefault(true); status.className = 'ok'; status.textContent = tr('Saved.');
    } catch (e) { status.className = 'errorText'; status.textContent = e.message; }
  };
  // ArgusSen's own limit (GB) – empty = no limit
  const limit = camInput(maxGB || '', { inputmode: 'numeric', placeholder: tr('No limit') });
  const limitStatus = el('p', { class: 'hint', role: 'status' });
  const usedText = el('p', { class: 'hint' }, d.used_gb >= 0
    ? (maxGB > 0 ? tr('ArgusSen uses {used} of {max}.', { used: gb(d.used_gb), max: gb(maxGB) }) : tr('ArgusSen uses {used}.', { used: gb(d.used_gb) }))
    : '');
  const limitBox = el('div', { style: 'display:flex;flex-direction:column;gap:10px' },
    el('h2', { class: 'small' }, tr('Most space for ArgusSen')),
    field(tr('Limit in GB'), limit, tr('Empty = no limit. When the recordings take more, the oldest are deleted first – never those around protected events.'), true),
    usedText,
    saveButton(tr('Save'), limitStatus, async () => {
      const r = await api('/api/admin/storage/limit', { method: 'PUT', body: { max_gb: +limit.value || 0 } });
      maxGB = r.max_gb || 0;
      limit.value = maxGB || '';
      if (d.used_gb >= 0) usedText.textContent = maxGB > 0 ? tr('ArgusSen uses {used} of {max}.', { used: gb(d.used_gb), max: gb(maxGB) }) : tr('ArgusSen uses {used}.', { used: gb(d.used_gb) });
      showEst(r.estimate_gb || 0);
    }), limitStatus);
  return el('div', { style: 'margin-top:16px;display:flex;flex-direction:column;gap:10px' },
    limitBox,
    el('h2', { class: 'small', style: 'margin-top:8px' }, tr('Keep recordings')), defText,
    el('div', { class: 'to' }, field(tr('Days of 24/7 (SD)'), sub), field(tr('Hours of full resolution (HD)'), main)),
    field(tr('Days of events'), evd, tr('Events keep their HD video. Protected events are never deleted.')),
    est,
    el('p', { class: 'hint do' }, tr('If the storage gets almost full, ArgusSen deletes the oldest recordings first – never those around protected events.')),
    saveButton(tr('Save'), status, async () => {
      const r = await api('/api/admin/retention', { method: 'PUT', body: { sub_days: +sub.value, main_hours: +main.value, event_days: +evd.value } });
      showEst(r.estimate_gb || 0); showDefault(r.retention_default);
    }), status, resetBtn);
}

SCREENS.set_storage = (root) => {
  const content = el('div', { class: 'content' });
  root.append(topBar(tr('Storage'), { back: true }), el('div', { class: 'page' }, content));
  const status = el('p', { class: 'errorText', role: 'alert' });
  const gb = (x) => x >= 1000 ? (x / 1000).toFixed(1).replace('.', ',') + ' TB' : Math.round(x) + ' GB';
  const shown = async () => {
    let d;
    try { d = await api('/api/admin/storage'); } catch (e) { content.replaceChildren(el('p', { class: 'errorText' }, e.message)); return; }
    let selected = d.current;
    const saveBtn = el('button', { type: 'button', class: 'btn green', disabled: true }, tr('Save and restart ArgusSen'));
    const row = d.places.map((p) => {
      const b = el('button', { type: 'button', class: 'menuRow' + (p.path === selected ? ' selected' : ''), disabled: !p.mounted,
        'aria-pressed': p.path === selected ? 'true' : 'false',
        onclick: () => {
          selected = p.path;
          for (const x of row) { const v = x.dataset.apiPath === selected; x.classList.toggle('selected', v); x.setAttribute('aria-pressed', v ? 'true' : 'false'); }
          saveBtn.disabled = selected === d.current;
        } },
      el('span', { class: 'mi', html: p.type === 'local' ? MENU_ICON.storage : MENU_ICON.network }),
      el('span', { class: 'mt' }, el('b', {}, (p.type === 'local' ? tr(p.name) : tr('Network drive {n}', { n: p.name.replace(/^Network drive /, '') })) + (p.current ? ' · ' + tr('in use') : '')),
        el('small', {}, !p.mounted ? tr('Not available right now') : tr('{free} free of {total}', { free: gb(p.free_gb), total: gb(p.total_gb) }) + ' · ' + p.path)));
      b.dataset.apiPath = p.path;
      return b;
    });
    saveBtn.onclick = async () => {
      if (!confirm(tr('ArgusSen will restart (about 15 seconds). Recordings in the old location are not moved: they can still be played back and are deleted when they expire. Continue?'))) return;
      saveBtn.disabled = true; status.className = 'ok'; status.textContent = tr('Saving …');
      try {
        const r = await api('/api/admin/storage', { method: 'PUT', body: { path: selected } });
        if (!r.restart) { shown(); return; }
        status.textContent = tr('ArgusSen is restarting …');
        const t0 = Date.now();
        await new Promise((ok) => setTimeout(ok, 5000));
        while (Date.now() - t0 < 90000) {
          try { const x = await fetch(S.server + '/api/me', { cache: 'no-store' }); if (x.ok) { location.reload(); return; } } catch (e) { /* */ }
          await new Promise((ok) => setTimeout(ok, 2000));
        }
        status.className = 'errorText'; status.textContent = tr('ArgusSen is not responding yet – reload the page in a moment.');
      } catch (e) { status.className = 'errorText'; status.textContent = e.message; saveBtn.disabled = false; }
    };
    if (!S.ha) { // the standalone server: the folder is a Docker volume, chosen outside ArgusSen
      content.replaceChildren(el('div', { style: 'display:flex;flex-direction:column;gap:6px' }, ...row),
        el('p', { class: 'hint do' }, tr('The recordings are stored in the folder that is mapped to /recordings (docker-compose.yml) – for example a NAS share mounted on the machine.')),
        retentionBox(d, gb));
      return;
    }
    content.replaceChildren(
      d.ok ? el('p', { class: 'hint lead' }, tr('Choose where new recordings are stored.'))
        : el('p', { class: 'errorText' }, tr('The storage is not available – recording is paused until the network drive is back.')),
      el('div', { style: 'display:flex;flex-direction:column;gap:6px' }, ...row),
      el('p', { class: 'hint do' }, tr('Network drives (NAS) are added in Home Assistant: Settings → System → Storage → Add network storage, with usage "Media". Recordings are stored in the folder "argus" on the drive.')),
      saveBtn, status, retentionBox(d, gb));
  };
  shown();
};

// cardify: a heading and what follows it become one card (section.setCard). A PC shows the cards in a grid of columns
// (the switches sit next to their text instead of 1500 px away); on a phone the cards are invisible (display: contents).
function cardify(content) {
  let card = null;
  for (const n of [...content.children]) {
    if (n.tagName === 'H2') { card = el('section', { class: 'setCard' }); content.insertBefore(card, n); }
    if (card) card.append(n);
  }
  content.classList.add('setGrid');
}

SCREENS.set_general = (root) => {
  const content = el('div', { class: 'content' });
  root.append(topBar(tr('General'), { back: true }), el('div', { class: 'page' }, content));
  // language – the page is reloaded so everything is drawn in the new language
  const langs = Object.entries(LANG_NAMES).filter(([k]) => k !== 'auto');
  const deviceSelect = el('select', { class: 'select', 'aria-label': tr('Language on this device') },
    el('option', { value: 'auto' }, tr('Same as ArgusSen ({name})', { name: LANG_NAMES[langCode('auto')] })),
    ...langs.map(([k, n]) => el('option', { value: k }, n)));
  deviceSelect.value = load('language', 'auto');
  deviceSelect.onchange = () => { save('language', deviceSelect.value); location.reload(); };
  if (S.user && S.user.admin) {
    // ArgusSen's own language: the app, the log and the names in Home Assistant
    const appSelect = el('select', { class: 'select', 'aria-label': tr('Language'), disabled: true });
    const langErr = el('p', { class: 'errorText', role: 'alert' });
    content.append(el('h2', { class: 'small' }, tr('Language in Home Assistant and the log')), appSelect,
      el('p', { class: 'hint' }, tr('The names of the entities in Home Assistant and the log – and the app\'s language on devices set to “Same as ArgusSen”.')), langErr);
    api('/api/language').then((l) => {
      const autoName = LANG_NAMES[l.auto] || l.auto;
      appSelect.append(el('option', { value: 'auto' }, l.ha ? tr('Automatic – the language of Home Assistant ({name})', { name: autoName })
        : tr('Automatic') + ' (' + autoName + ')'), ...langs.map(([k, n]) => el('option', { value: k }, n)));
      appSelect.value = l.setting; appSelect.disabled = false;
    }).catch((e) => { langErr.textContent = e.message; });
    appSelect.onchange = async () => {
      appSelect.disabled = true;
      try {
        const l = await api('/api/admin/language', { method: 'PUT', body: { setting: appSelect.value } });
        save('serverLanguage', l.language); save('language', 'auto'); location.reload();
      } catch (e) { langErr.textContent = e.message; appSelect.disabled = false; }
    };
    content.append(el('h2', { class: 'small', style: 'margin-top:8px' }, tr('Language in the app on this device')), deviceSelect);
    // camera images to Home Assistant (MQTT): everybody with access to the MQTT broker can read them
    const haImg = el('div');
    content.append(el('h2', { class: 'small', style: 'margin-top:8px' }, 'Home Assistant'), haImg);
    api('/api/admin/ha').then((o) => haImg.replaceChildren(toggleButton(tr('Camera images in Home Assistant'),
      tr('A still image of each camera and the latest event are sent via MQTT. Everybody with access to the MQTT broker can see them.'), o.images,
      async (t) => { try { await api('/api/admin/ha', { method: 'PUT', body: { images: t } }); } catch (e) { alert(e.message); } }))).catch(() => {});
  } else {
    content.append(el('h2', { class: 'small' }, tr('Language in the app on this device')), deviceSelect);
  }
  const opt = (camKey) => (until) => { S.opt[camKey] = until; save('opt', S.opt); };
  content.append(el('h2', { class: 'small', style: 'margin-top:8px' }, tr('Display (this device)')),
    toggleButton(tr('Show yellow boxes on events'), tr('Frame around person, car … in images'), S.opt.boxes, opt('boxes')),
    toggleButton(tr('Show boxes in live view'), tr('Draws AI detections on top of the live video'), S.opt.liveBoxes, opt('liveBoxes')),
    toggleButton(tr('Events in SD on mobile data'), tr('Starts faster and uses less data – tap HD for full resolution'), S.opt.mobileSD, opt('mobileSD')),
    toggleButton(tr('Events as a time axis'), tr('On a PC: the events along a time axis down the middle. Off = the classic list.'), S.opt.timeAxis, opt('timeAxis')));
  // the order of the cameras: one's own (made by moving them in the overview) or the default
  const orderBox = el('div', { style: 'display:flex;flex-direction:column;gap:6px' });
  const drawOrder = (own) => orderBox.replaceChildren(el('p', { class: 'hint' }, own ? tr('You use your own order of the cameras.')
    : tr('Hold a camera in the overview and drag it to put the cameras in your own order (only for you).')),
  own ? el('button', { type: 'button', class: 'btn grey', onclick: async () => {
    try { await api('/api/me/order', { method: 'PUT', body: { ids: [] } }); drawOrder(false); fetchCameras(); } catch (e) { alert(e.message); }
  } }, tr('Use the default order')) : null);
  content.append(el('h2', { class: 'small', style: 'margin-top:8px' }, tr('Camera order')), orderBox);
  drawOrder(false);
  api('/api/me').then((m) => drawOrder(!!m.own_order)).catch(() => {});
  const errorText = el('p', { class: 'errorText', role: 'alert' });
  const oldPw = el('input', { type: 'password', autocomplete: 'current-password' });
  const fresh = el('input', { type: 'password', autocomplete: 'new-password' });
  const fresh2 = el('input', { type: 'password', autocomplete: 'new-password' });
  const form = el('form', { class: 'panelBox', hidden: true, onsubmit: async (ev) => {
    ev.preventDefault();
    errorText.className = 'errorText'; errorText.textContent = '';
    if (fresh.value !== fresh2.value) { errorText.textContent = tr('The two new passwords do not match'); return; }
    try {
      await api('/api/password', { method: 'POST', body: { old: oldPw.value, new: fresh.value }, noLogout: true });
      errorText.className = 'ok'; errorText.textContent = tr('Password changed. Other phones have been logged out.');
      oldPw.value = fresh.value = fresh2.value = '';
    } catch (e) { errorText.textContent = e.message; }
  } },
  field(tr('Current password'), oldPw), field(tr('New password (at least 8 characters)'), fresh), field(tr('New password again'), fresh2),
  errorText, el('button', { type: 'submit', class: 'btn green' }, tr('Save new password')));
  if (S.ha) content.append(el('h2', { class: 'small', style: 'margin-top:8px' }, tr('My account') + ' · ' + (S.user ? S.user.name : '')),
    el('p', { class: 'hint' }, tr('You are logged in through Home Assistant.')));
  else content.append(el('h2', { class: 'small', style: 'margin-top:8px' }, tr('My account') + ' · ' + (S.user ? S.user.name : '')),
    el('div', { class: 'to' },
      el('button', { type: 'button', class: 'btn grey', onclick: () => { form.hidden = !form.hidden; } }, tr('Change password')),
      el('button', { type: 'button', class: 'btn grey', onclick: () => logout(false) }, tr('Log out'))),
    form);
  content.append(
    el('h2', { class: 'small', style: 'margin-top:8px' }, tr('App')),
    el('p', { class: 'hint' }, tr('Version') + ' ' + (NATIVE ? NATIVE.versionName() : 'web') + (S.ha ? '' : ' · server ' + S.server.replace(/^https?:\/\//, ''))),
    el('button', { type: 'button', class: 'btn grey', onclick: () => checkVersion(true) }, tr('Check for update')));
  cardify(content);
};

// ---------- users ----------
SCREENS.set_users = (root, p, ctx) => {
  const list = el('div', { style: 'display:flex;flex-direction:column;gap:6px' });
  root.append(topBar(tr('Users'), { back: true }), el('div', { class: 'page' }, el('div', { class: 'content' },
    el('button', { type: 'button', class: 'btn green', html: MENU_ICON.plus + tr('Add user'), onclick: () => go('set_user', {}) }), list)));
  (async () => {
    try {
      const [us] = await Promise.all([api('/api/admin/users'), fetchCameras()]);
      // as Home Assistant app: what new HA users may see (administrators always see everything)
      const haBox = S.ha ? el('div', { style: 'margin-top:12px;display:flex;flex-direction:column;gap:6px' }) : null;
      if (S.ha) {
        try {
          const o = await api('/api/admin/ha');
          haBox.append(toggleButton(tr('New Home Assistant users see all cameras'),
            tr('Otherwise they see no cameras until you choose some here. Home Assistant administrators always see all.'), o.all_cams,
            async (t) => { try { await api('/api/admin/ha', { method: 'PUT', body: { all_cams: t } }); } catch (e) { alert(e.message); } }));
        } catch (e) { /* older server */ }
      }
      list.replaceChildren(...us.map((u) => menuRow(MENU_ICON.userList, u.name + (u.admin ? ' · admin' : '') + (u.ha ? ' · Home Assistant' : '') + (u.self ? ' (' + tr('you') + ')' : ''),
        (u.cams.includes('*') ? tr('All cameras') : u.cams.length ? u.cams.map(camName).join(', ') : tr('No cameras')) + ' · ' + tr(u.devices === 1 ? '{n} device' : '{n} devices', { n: u.devices }),
        () => go('set_user', { name: u.name }))), haBox);
    } catch (e) { list.replaceChildren(el('p', { class: 'errorText' }, e.message)); }
  })();
};

// camera choices: "All cameras" + one choice per camera
function cameraPicker(selectedSet) {
  const allItems = allCams();
  let st = new Set(selectedSet);
  const box = el('div', { style: 'display:flex;flex-direction:column;gap:8px' });
  const draw = () => {
    const isAll = st.has('*');
    box.replaceChildren(
      toggleButton(tr('All cameras'), tr('Also cameras added later'), isAll, (until) => { st = until ? new Set(['*']) : new Set(); draw(); }),
      isAll ? null : el('div', { class: 'chips' }, ...allItems.map((c) => el('button', { type: 'button', class: 'chip' + (st.has(c.id) ? ' selected' : ''),
        'aria-pressed': st.has(c.id) ? 'true' : 'false', onclick: () => { st.has(c.id) ? st.delete(c.id) : st.add(c.id); draw(); } }, c.name))));
  };
  draw();
  return { el: box, value: () => [...st] };
}
let CAMERA_LIST = [];
const allCams = () => (CAMERA_LIST.length ? CAMERA_LIST : S.cams);

SCREENS.set_user = (root, p) => {
  const fresh = !p.name;
  const content = el('div', { class: 'content' });
  root.append(topBar(fresh ? tr('New user') : p.name, { back: true }), el('div', { class: 'page' }, content));
  (async () => {
    let u = { name: '', admin: false, cams: [], self: false };
    try {
      CAMERA_LIST = await api('/api/admin/cameras');
      if (!fresh) u = (await api('/api/admin/users')).find((x) => x.name === p.name) || u;
    } catch (e) { content.append(el('p', { class: 'errorText' }, e.message)); return; }
    const name = el('input', { type: 'text', autocapitalize: 'none', value: u.name });
    const pw = el('input', { type: 'password', autocomplete: 'new-password', placeholder: fresh ? '' : tr('Unchanged') });
    let admin = u.admin;
    const kv = cameraPicker(u.cams);
    const status = el('p', { class: 'hint', role: 'status' });
    content.append(
      fresh ? field(tr('Username'), name) : null,
      field(fresh ? tr('Password (at least 8 characters)') : tr('New password (empty = unchanged)'), pw, fresh ? null : u.ha ? tr('Only needed to log in from the ArgusSen phone app – in Home Assistant the user is already logged in.') : tr('The user\'s phones are logged out when the password changes')),
      u.self ? el('p', { class: 'hint do' }, tr('This is you – your own admin rights cannot be removed.')) :
        u.ha ? el('p', { class: 'hint' }, tr(u.admin ? 'Administrator in Home Assistant – admin rights are set in Home Assistant.' : 'Not an administrator in Home Assistant – admin rights are set in Home Assistant.')) :
        toggleButton(tr('Admin'), tr('Can edit cameras, users and settings'), admin, (t) => { admin = t; }),
      el('h2', { class: 'small', style: 'margin-top:6px' }, tr('May see')), kv.el,
      el('button', { type: 'button', class: 'btn green', onclick: async () => {
        status.className = 'hint'; status.textContent = tr('Saving …');
        try {
          if (fresh) await api('/api/admin/users', { method: 'POST', body: { name: name.value.trim(), password: pw.value, admin, cams: kv.value() } });
          else await api('/api/admin/users/' + encodeURIComponent(u.name), { method: 'PUT', body: { admin: u.self || u.ha ? undefined : admin, cams: kv.value(), password: pw.value || undefined } });
          back();
        } catch (e) { status.className = 'errorText'; status.textContent = e.message; }
      } }, fresh ? tr('Create user') : tr('Save')),
      status);
    if (!fresh) {
      content.append(el('div', { class: 'to', style: 'margin-top:10px' },
        el('button', { type: 'button', class: 'btn grey', onclick: async () => {
          try { await api('/api/admin/users/' + encodeURIComponent(u.name) + '/logout', { method: 'POST' }); status.className = 'ok'; status.textContent = tr('{name} has been logged out on all devices.', { name: u.name }); } catch (e) { status.textContent = e.message; }
        } }, tr('Log out everywhere')),
        u.self ? null : el('button', { type: 'button', class: 'btn grey', onclick: async () => {
          if (!confirm(tr('Delete the user {name}?', { name: u.name }) + (u.ha ? '\n' + tr('A Home Assistant user comes back the next time it opens ArgusSen (with the cameras new Home Assistant users get). Remove the user in Home Assistant instead.') : ''))) return;
          try { await api('/api/admin/users/' + encodeURIComponent(u.name), { method: 'DELETE' }); back(); } catch (e) { status.textContent = e.message; }
        } }, tr('Delete user'))));
    }
  })();
};

// ---------- cameras ----------
SCREENS.set_cameras = (root) => {
  const list = el('div', { style: 'display:flex;flex-direction:column;gap:6px' });
  const orderBtn = el('button', { type: 'button', class: 'btn grey', hidden: true, onclick: () => orderMode() }, tr('Change order'));
  root.append(topBar(tr('Cameras'), { back: true }), el('div', { class: 'page' }, el('div', { class: 'content' },
    el('button', { type: 'button', class: 'btn green', html: MENU_ICON.plus + tr('Add camera'), onclick: () => go('set_cam_conn', {}) }), list, orderBtn)));
  // the order is used everywhere (overview, live, timeline) and for all users
  function orderMode() {
    const ids = CAMERA_LIST.map((c) => c.id);
    const status = el('p', { class: 'hint', role: 'status' });
    const draw = () => list.replaceChildren(el('p', { class: 'hint do' }, tr('Move the cameras up or down, and press Save.')),
      el('p', { class: 'hint' }, tr('This is the default order – for everybody who has not moved the cameras in their own overview.')),
      ...ids.map((id, i) => {
        const c = CAMERA_LIST.find((x) => x.id === id);
        const move = (d) => { [ids[i], ids[i + d]] = [ids[i + d], ids[i]]; draw(); };
        return el('div', { class: 'camRow' }, el('b', { style: 'flex:1' }, c.name),
          el('button', { type: 'button', class: 'btn grey orderBtn', disabled: i === 0, 'aria-label': tr('Move up'), onclick: () => move(-1) }, '↑'),
          el('button', { type: 'button', class: 'btn grey orderBtn', disabled: i === ids.length - 1, 'aria-label': tr('Move down'), onclick: () => move(1) }, '↓'));
      }),
      saveButton(tr('Save'), status, async () => {
        await api('/api/admin/cameras/order', { method: 'PUT', body: { ids } });
        await fetchCameras();
        go('set_cameras', {}, true);
      }), status);
    orderBtn.hidden = true;
    draw();
  }
  (async () => {
    try {
      CAMERA_LIST = await api('/api/admin/cameras');
      orderBtn.hidden = CAMERA_LIST.length < 2;
      list.replaceChildren(...CAMERA_LIST.map((c) => {
        const status = !c.enabled ? tr('disabled') : c.sub_ok ? tr('recording via {m}', { m: (c.main_via || '?') + (c.sub_via && c.sub_via !== c.main_via ? '/' + c.sub_via : '') }) : (c.optional ? tr('switched off') : tr('no connection'));
        const r = menuRow(MENU_ICON.cameras, c.name, c.host + ' · ' + status, () => go('set_camera', { id: c.id }));
        r.querySelector('.mi').prepend(el('span', { class: 'dot' + (c.sub_ok || !c.enabled || c.optional ? '' : ' off'), style: 'position:absolute;margin:-14px 0 0 18px' + (!c.enabled ? ';background:#777' : '') }));
        return r;
      }));
    } catch (e) { list.replaceChildren(el('p', { class: 'errorText' }, e.message)); }
  })();
};

const METHODS = [
  ['auto', 'Automatic (bc → rtsp → flv)'], ['bc,flv,rtsp', 'bc, then flv, then rtsp'], ['flv,bc,rtsp', 'flv, then bc, then rtsp'],
  ['rtsp,bc,flv', 'rtsp, then bc, then flv'], ['rtsp,bc', 'rtsp, then bc'], ['bc', 'bc only'], ['flv', 'flv only'], ['rtsp', 'rtsp only'],
];

// A camera's settings are split into four sections, each with its own Save button:
// 1 Connection · 2 Who may see it and operation · 3 Home Assistant · 4 Motion and AI.
// The server wants all fields on every save, so each section sends the camera's current values + its own changes.
async function fetchCamera(id) {
  CAMERA_LIST = await api('/api/admin/cameras');
  return CAMERA_LIST.find((x) => x.id === id) || null;
}
function camBody(c, over) {
  return Object.assign({ id: c.id, name: c.name, host: c.host, port: c.port || 554, bc_port: c.bc_port || 9000, protos: c.protos,
    kind: c.kind, onvif_port: c.onvif_port, main_path: c.kind === 'reolink' ? '' : c.main_path, sub_path: c.kind === 'reolink' ? '' : c.sub_path,
    enabled: c.enabled, optional: c.optional, ha_name: c.ha_name, ha_area: c.ha_area, user: c.user, pass: '' }, over);
}
function saveButton(text, status, save) {
  const k = el('button', { type: 'button', class: 'btn green', style: 'margin-top:8px', onclick: async () => {
    k.disabled = true; status.className = 'hint'; status.textContent = tr('Saving …');
    try { await save(); status.className = 'ok'; status.textContent = tr('Saved.'); } catch (e) { status.className = 'errorText'; status.textContent = e.message; }
    k.disabled = false;
  } }, text);
  return k;
}
function camPage(root, heading2) {
  const content = el('div', { class: 'content' });
  root.append(topBar(heading2, { back: true }), el('div', { class: 'page' }, content));
  return content;
}
const camInput = (v, attrs) => el('input', Object.assign({ type: 'text', class: 'inp', value: v == null ? '' : v, autocapitalize: 'none' }, attrs || {}));

// the menu for one camera (a new camera starts in Connection)
SCREENS.set_camera = (root, p) => {
  if (!p.id) { go('set_cam_conn', {}, true); return; }
  const content = camPage(root, tr('Camera'));
  (async () => {
    let c, d = null;
    try { c = await fetchCamera(p.id); d = await api('/api/settings'); } catch (e) { content.append(el('p', { class: 'errorText' }, e.message)); return; }
    if (!c) { content.append(el('p', { class: 'errorText' }, tr('The camera does not exist'))); return; }
    $('.top h1', root).firstChild.textContent = c.name;
    const methodSelect = c.kind === 'onvif' ? 'ONVIF' : c.kind === 'rtsp' ? 'RTSP' : tr((METHODS.find(([v]) => v === c.protos) || [c.protos, c.protos])[1]);
    const viewers = c.users.filter((n) => !n.endsWith(' (all)'));
    const st = d && d.cameras.find((x) => x.id === c.id);
    content.append(
      menuRow(MENU_ICON.network, tr('1 · Connection'), c.host + ' · ' + methodSelect, () => go('set_cam_conn', { id: c.id })),
      menuRow(MENU_ICON.userList, tr('2 · Who may see it and operation'), (c.enabled ? tr('Active') : tr('Disabled')) + (c.optional ? ', ' + tr('may be switched off') : '') + ' · ' + (viewers.length ? viewers.join(', ') : tr('only those who see all')), () => go('set_cam_access', { id: c.id })),
      menuRow(MENU_ICON.ha, tr('3 · Home Assistant'), (c.ha_name || c.name) + (c.ha_area ? ' · ' + c.ha_area : ''), () => go('set_cam_ha', { id: c.id })),
      menuRow(MENU_ICON.ai, tr('4 · Motion and AI'), st ? tr('Sensitivity {n}', { n: st.sensitivity }) + ' · ' + (st.objects.length ? st.objects.map(labelName).join(', ') : tr('no events')) : tr('The camera is not running'), () => go('set_cam_ai', { id: c.id })),
      menuRow(MENU_ICON.zones, tr('5 · Zones'), st ? zoneSummary(st.zones) : tr('The camera is not running'), () => st && go('set_cam_zones', { id: c.id })),
      el('button', { type: 'button', class: 'btn grey', style: 'margin-top:14px', onclick: async () => {
        if (!confirm(tr('Delete the camera {name}?', { name: c.name }) + '\n' + tr('The recordings stay until they expire (14/30 days).'))) return;
        try { await api('/api/admin/cameras/' + c.id, { method: 'DELETE' }); await fetchCameras(); back(); } catch (e) { alert(e.message); }
      } }, tr('Delete camera')));
  })();
};

// 1 · Connection (also creating a new camera)
// Camera types: Reolink (bc/flv/rtsp, optimised), ONVIF (most other brands – the addresses are found by ArgusSen) and
// RTSP (addresses typed in, with presets for common brands). New cameras can be found with "Search the network".
const CAM_KINDS = [['reolink', 'Reolink'], ['onvif', 'ONVIF (most other brands)'], ['rtsp', 'RTSP address (manual)']];
// RTSP paths for common brands: [name, port, main stream, sub stream]
const RTSP_PRESETS = [
  ['Hikvision / Annke / HiLook', 554, '/Streaming/Channels/101', '/Streaming/Channels/102'],
  ['Dahua / Amcrest / Lorex / Imou', 554, '/cam/realmonitor?channel=1&subtype=0', '/cam/realmonitor?channel=1&subtype=1'],
  ['Axis', 554, '/axis-media/media.amp?videocodec=h264', '/axis-media/media.amp?videocodec=h264&resolution=640x480'],
  ['TP-Link Tapo / VIGI', 554, '/stream1', '/stream2'],
  ['Uniview', 554, '/media/video1', '/media/video2'],
  ['Foscam', 88, '/videoMain', '/videoSub'],
  ['Ubiquiti UniFi Protect', 7441, '', ''],
];
SCREENS.set_cam_conn = (root, p) => {
  const fresh = !p.id;
  const content = camPage(root, fresh ? tr('New camera') : tr('Connection'));
  (async () => {
    let c = { name: '', host: '', port: 554, bc_port: 9000, protos: 'auto', kind: 'reolink', onvif_port: 80, main_path: '', sub_path: '',
      enabled: true, optional: false, ha_name: '', ha_area: '', user: 'admin', has_password: false, users: [] };
    if (!fresh) { try { c = await fetchCamera(p.id) || c; } catch (e) { content.append(el('p', { class: 'errorText' }, e.message)); return; } }
    if (p.found) Object.assign(c, p.found);
    const name = camInput(c.name, { autocapitalize: 'sentences' });
    const host = camInput(c.host, { inputmode: 'url', placeholder: tr('e.g. 192.168.1.20') });
    const port = camInput(c.port, { inputmode: 'numeric' });
    const bcport = camInput(c.bc_port, { inputmode: 'numeric' });
    const onvifPort = camInput(c.onvif_port || 80, { inputmode: 'numeric' });
    const mainPath = camInput(c.kind === 'reolink' ? '' : c.main_path, { placeholder: '/stream1' });
    const subPath = camInput(c.kind === 'reolink' ? '' : c.sub_path, { placeholder: '/stream2' });
    const userField = camInput(c.user);
    const pw = camInput('', { type: 'password', autocomplete: 'new-password', placeholder: c.has_password ? tr('Unchanged') : '' });
    const kindSelect = el('select', { class: 'select' }, ...CAM_KINDS.map(([v, t]) => el('option', { value: v }, tr(t))));
    kindSelect.value = c.kind || 'reolink';
    const presetSelect = el('select', { class: 'select' }, el('option', { value: '' }, tr('Choose brand …')),
      ...RTSP_PRESETS.map(([n], i) => el('option', { value: String(i) }, n)), el('option', { value: 'other' }, tr('Other brand')));
    presetSelect.onchange = () => {
      const pr = RTSP_PRESETS[+presetSelect.value];
      if (!pr) return;
      port.value = pr[1]; mainPath.value = pr[2]; subPath.value = pr[3];
      if (!pr[2]) mainPath.placeholder = subPath.placeholder = 'rtsps://…';
    };
    const methodSelect = el('select', { class: 'select' }, ...METHODS.map(([v, t]) => el('option', { value: v }, tr(t))));
    if (c.protos && !METHODS.some(([v]) => v === c.protos)) methodSelect.append(el('option', { value: c.protos }, c.protos));
    methodSelect.value = c.kind === 'reolink' ? (c.protos || 'auto') : 'auto';
    const over = () => {
      const k = kindSelect.value;
      return { name: name.value, host: host.value, port: +port.value || 554, bc_port: +bcport.value || 9000, protos: methodSelect.value,
        kind: k, onvif_port: +onvifPort.value || 80, main_path: k === 'reolink' ? '' : mainPath.value.trim(), sub_path: k === 'reolink' ? '' : subPath.value.trim(),
        user: userField.value, pass: pw.value };
    };
    const status = el('p', { class: 'hint', role: 'status' });
    const testResult = el('div', { style: 'display:flex;flex-direction:column;gap:6px' });
    const STEP = { onvif: 'ONVIF', main: tr('Main stream'), sub: tr('Sub stream') };
    const test = el('button', { type: 'button', class: 'btn grey', onclick: async () => {
      const k = kindSelect.value;
      test.disabled = true;
      testResult.replaceChildren(el('p', { class: 'hint' }, k === 'reolink' ? tr('Trying bc, flv and rtsp … (up to 1 minute)') : tr('Testing … (up to 1 minute)')));
      try {
        const d = await api('/api/admin/cameras/test', { method: 'POST', body: camBody(c, over()) });
        const rows = d.results.map((r) => el('div', { class: 'camRow' },
          el('span', { class: 'dot' + (r.ok ? '' : ' off') }), el('b', {}, STEP[r.proto] || r.proto),
          el('span', {}, r.ok ? tr('works') + (r.size ? ' · ' + r.size : '') + ' · ' + (r.ms / 1000).toFixed(1) + ' s' : tr(r.error))));
        const img = d.image ? el('img', { src: d.image, alt: tr('Test image'), style: 'width:100%;border-radius:6px' }) : null;
        // concrete things to check when something failed
        const TIPS = {
          reolink: ['Use the username and password from the Reolink app.', 'In the camera (Reolink app → Settings → Network → Advanced → Server settings): switch on RTSP and HTTPS.', 'Only wired cameras can record 24/7 – battery cameras are not supported.'],
          onvif: ['ONVIF must be switched on in the camera, often under Network or Integration.', 'Try the other usual ONVIF ports: 80, 8000, 8080, 2020.', 'Some brands (e.g. Hikvision, TP-Link) need a separate ONVIF user, created in the camera.', 'If the camera\'s clock is very wrong, ONVIF login can fail – set the time in the camera.'],
          rtsp: ['RTSP must be switched on in the camera (often port 554).', 'Check the stream paths in the camera\'s manual – they differ between models.', 'The sub stream must be at most about 1.3 megapixels (e.g. 1280×960).'],
        };
        const tips = d.results.some((x) => !x.ok) ? el('div', { class: 'hint' }, el('b', {}, tr('Things to check:')),
          el('ul', { style: 'margin:4px 0 0 18px;padding:0' }, ...TIPS[k].map((t) => el('li', {}, tr(t))))) : null;
        if (k === 'reolink') {
          testResult.replaceChildren(...rows, img,
            d.protos ? el('button', { type: 'button', class: 'btn grey', onclick: () => {
              if (!METHODS.some(([v]) => v === d.protos) && ![...methodSelect.options].some((o) => o.value === d.protos)) methodSelect.append(el('option', { value: d.protos }, d.protos));
              methodSelect.value = d.protos; status.className = 'ok'; status.textContent = tr('Methods set to: {m} – remember to save.', { m: d.protos });
            } }, tr('Use the methods that work ({m})', { m: d.protos })) : el('p', { class: 'errorText' }, tr('None of the methods worked – check address, username and password.')), tips);
        } else {
          if (d.main_path) { mainPath.value = d.main_path; subPath.value = d.sub_path || ''; }
          if (k === 'onvif' && d.model && !name.value) name.value = d.model;
          testResult.replaceChildren(...rows, img,
            d.warning ? el('p', { class: 'hint do' }, tr('The camera does not send H.264. Recording works, but live view and playback need a browser that can show H.265 – choose H.264 in the camera if possible.')) : null,
            k === 'onvif' && d.main_path ? el('p', { class: 'hint' }, tr('Addresses found: {main} and {sub}', { main: d.main_path, sub: d.sub_path })) : null, tips);
        }
      } catch (e) { testResult.replaceChildren(el('p', { class: 'errorText' }, e.message)); }
      test.disabled = false;
    } }, tr('Test connection'));
    // the fields that depend on the camera type
    const typeFields = el('div', { style: 'display:flex;flex-direction:column;gap:10px' });
    const shown = () => {
      const k = kindSelect.value;
      if (k === 'reolink') {
        typeFields.replaceChildren(
          el('div', { class: 'to' }, field(tr('RTSP port'), port), field(tr('Reolink port (bc)'), bcport)),
          field(tr('Method'), methodSelect, tr('bc = Reolink\'s own protocol, flv = HTTPS-FLV, rtsp = standard. ArgusSen switches to the next one by itself if a method fails.')));
      } else if (k === 'onvif') {
        typeFields.replaceChildren(
          field(tr('ONVIF port'), onvifPort, tr('Often 80, 8000 or 8080. ONVIF must be switched on in the camera – some cameras need a separate ONVIF user.'), true),
          el('p', { class: 'hint do' }, tr('ArgusSen finds the addresses of the main stream (full resolution) and the sub stream (low resolution) itself – press Test connection.')));
      } else {
        typeFields.replaceChildren(
          field(tr('Brand'), presetSelect, tr('Fills in the usual addresses for the brand – check them in the camera\'s manual if the test fails.')),
          field(tr('RTSP port'), port),
          field(tr('Main stream (full resolution)'), mainPath, tr('Path such as /stream1 or a whole address rtsp://…')),
          field(tr('Sub stream (low resolution)'), subPath, tr('Used for motion detection, the timeline and live view on phones – at most about 1.3 megapixels (e.g. 1280×960).'), true));
      }
    };
    kindSelect.onchange = shown;
    shown();
    // search the network for cameras (only when creating a new camera)
    const found = el('div', { style: 'display:flex;flex-direction:column;gap:6px' });
    const rangeInput = camInput('', { placeholder: tr('e.g. 192.168.1.0/24'), inputmode: 'url' });
    const search = el('button', { type: 'button', class: 'btn grey', onclick: async () => {
      search.disabled = true; found.replaceChildren(el('p', { class: 'hint' }, tr('Searching the network … (about 10 seconds)')));
      try {
        const q = rangeInput.value.trim() ? '?range=' + encodeURIComponent(rangeInput.value.trim()) : '';
        const d = await api('/api/admin/cameras/discover' + q);
        const list = d.cameras.map((f) => {
          const label = [f.brand, f.model].filter(Boolean).join(' ') || tr('Unknown camera');
          const via = f.kind === 'reolink' ? 'Reolink' : f.kind === 'onvif' ? 'ONVIF' : 'RTSP';
          const row = menuRow(ICON.camera, f.host + ' · ' + label, f.added ? tr('Already added as {name}', { name: f.added }) : via, () => {
            const k = f.kind === 'reolink' ? 'reolink' : f.kind === 'onvif' ? 'onvif' : 'rtsp';
            host.value = f.host; kindSelect.value = k; if (f.onvif_port) onvifPort.value = f.onvif_port;
            shown(); found.replaceChildren(el('p', { class: 'ok' }, tr('{name} chosen – fill in username and password and press Test connection.', { name: f.host })));
            if (!name.value && f.model) name.value = f.model;
          });
          row.disabled = !!f.added;
          return row;
        });
        found.replaceChildren(
          list.length ? el('div', { style: 'display:flex;flex-direction:column;gap:6px' }, ...list) : el('p', { class: 'hint' }, tr('No cameras found.')),
          el('p', { class: 'hint do' }, tr('Searched: {nets}. If your cameras are on another network (for example a separate camera network/VLAN), type it here – Home Assistant must be able to reach it:', { nets: (d.searched || []).join(', ') })),
          el('div', { class: 'to' }, rangeInput, el('button', { type: 'button', class: 'btn grey', onclick: () => search.click() }, tr('Search'))));
      } catch (e) { found.replaceChildren(el('p', { class: 'errorText' }, e.message), el('div', { class: 'to' }, rangeInput, el('button', { type: 'button', class: 'btn grey', onclick: () => search.click() }, tr('Search')))); }
      search.disabled = false;
    } }, tr('Search the network for cameras'));
    content.append(
      fresh ? search : null, fresh ? found : null,
      field(tr('Camera type'), kindSelect),
      field(tr('Name'), name), field(tr('Address (IP or name)'), host),
      el('div', { class: 'to' }, field(tr('Username'), userField), field(tr('Password'), pw)),
      typeFields,
      test, testResult,
      saveButton(fresh ? tr('Create camera') : tr('Save'), status, async () => {
        if (fresh) {
          const r = await api('/api/admin/cameras', { method: 'POST', body: camBody(c, over()) });
          await fetchCameras();
          go('set_camera', { id: r.id }, true); // on to the camera's menu
          return;
        }
        Object.assign(c, await api('/api/admin/cameras/' + c.id, { method: 'PUT', body: camBody(c, over()) }));
        pw.value = ''; pw.placeholder = tr('Unchanged');
        await fetchCameras();
      }),
      status,
      fresh ? el('p', { class: 'hint do' }, tr('Once the camera is created, you can choose who may see it, the Home Assistant name and motion/AI.')) : null);
  })();
};

// 2 · Who may see it and operation
SCREENS.set_cam_access = (root, p) => {
  const content = camPage(root, tr('Who may see it and operation'));
  (async () => {
    let c, userList;
    try { c = await fetchCamera(p.id); userList = await api('/api/admin/users'); } catch (e) { content.append(el('p', { class: 'errorText' }, e.message)); return; }
    let enabled = c.enabled, optional = c.optional;
    const selectedSet = new Set(c.users.filter((n) => !n.endsWith(' (all)')));
    const others = userList.filter((u) => !u.cams.includes('*'));
    const seesAll = userList.filter((u) => u.cams.includes('*')).map((u) => u.name);
    const viewersBox = el('div', { class: 'chips' });
    const drawViewers = () => viewersBox.replaceChildren(...others.map((u) => el('button', { type: 'button', class: 'chip' + (selectedSet.has(u.name) ? ' selected' : ''),
      'aria-pressed': selectedSet.has(u.name) ? 'true' : 'false', onclick: () => { selectedSet.has(u.name) ? selectedSet.delete(u.name) : selectedSet.add(u.name); drawViewers(); } }, u.name)));
    drawViewers();
    const status = el('p', { class: 'hint', role: 'status' });
    content.append(
      el('h2', { class: 'small' }, tr('Who may see the camera')),
      seesAll.length ? el('p', { class: 'hint' }, tr('Sees all cameras: {names}', { names: seesAll.join(', ') })) : null,
      others.length ? viewersBox : el('p', { class: 'hint' }, tr('No other users')),
      el('h2', { class: 'small', style: 'margin-top:8px' }, tr('Operation')),
      toggleButton(tr('Active'), tr('Records and creates events'), enabled, (t) => { enabled = t; }),
      toggleButton(tr('May be switched off'), tr('Switched on and off as needed – no alert when it is gone'), optional, (t) => { optional = t; }),
      saveButton(tr('Save'), status, async () => {
        Object.assign(c, await api('/api/admin/cameras/' + c.id, { method: 'PUT', body: camBody(c, { enabled, optional, users: [...selectedSet] }) }));
        await fetchCameras();
      }),
      status);
  })();
};

// 3 · Home Assistant
SCREENS.set_cam_ha = (root, p) => {
  const content = camPage(root, tr('Home Assistant'));
  (async () => {
    let c;
    try { c = await fetchCamera(p.id); } catch (e) { content.append(el('p', { class: 'errorText' }, e.message)); return; }
    const haName = camInput(c.ha_name, { autocapitalize: 'sentences', placeholder: c.name });
    const haArea = camInput(c.ha_area, { autocapitalize: 'sentences', placeholder: tr('e.g. Outside') });
    const status = el('p', { class: 'hint', role: 'status' });
    content.append(
      field(tr('Name in Home Assistant'), haName, tr('The device is called "Camera <name>". Empty = the camera\'s name.')),
      field(tr('Area'), haArea, tr('Suggested area in Home Assistant.')),
      saveButton(tr('Save'), status, async () => {
        Object.assign(c, await api('/api/admin/cameras/' + c.id, { method: 'PUT', body: camBody(c, { ha_name: haName.value, ha_area: haArea.value }) }));
      }),
      status);
  })();
};

// 4 · Motion and AI
// ---------- zones ----------
// Up to 3 zones and 3 areas to ignore per camera, drawn as polygons on a picture from the camera. The points are saved
// in the camera's own orientation (0–1); a camera shown rotated 180° is turned here when drawing.
const ZONE_COLOURS = ['#5CC8BB', '#F59A3C', '#B78CF0'];
const IGNORE_COLOUR = '#D63A2C';
function zoneSummary(zones) {
  const z = (zones || []).filter((x) => x.kind === 'zone').length, i = (zones || []).length - z;
  if (!z && !i) return tr('No zones');
  return tr('{z} zones, {i} areas to ignore', { z, i });
}
function zoneColour(zones, zone) {
  if (zone.kind === 'ignore') return IGNORE_COLOUR;
  return ZONE_COLOURS[(parseInt((zone.id || 'z1').slice(1), 10) - 1) % 3] || ZONE_COLOURS[0];
}
// zoneName: the name of a zone on a camera (for the events list)
function zoneName(cam, id) {
  const z = ((S.cams.find((c) => c.id === cam) || {}).zones || []).find((x) => x.id === id);
  return z ? z.name : '';
}
// zoneSVG: the zones as an SVG over a picture (viewBox 0–1000; the picture's own orientation)
function zoneSVG(zones, editing) {
  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('viewBox', '0 0 1000 1000');
  svg.setAttribute('preserveAspectRatio', 'none');
  svg.setAttribute('class', 'zones');
  for (const z of zones) {
    if (!z.points || z.points.length < 2) continue;
    const col = zoneColour(zones, z);
    const poly = document.createElementNS(NS, z.points.length > 2 ? 'polygon' : 'polyline');
    poly.setAttribute('points', z.points.map(([x, y]) => (x * 1000).toFixed(1) + ',' + (y * 1000).toFixed(1)).join(' '));
    poly.setAttribute('fill', col);
    poly.setAttribute('fill-opacity', z === editing ? '0.30' : (z.kind === 'ignore' ? '0.35' : '0.18'));
    poly.setAttribute('stroke', col);
    poly.setAttribute('stroke-width', z === editing ? '6' : '4');
    poly.setAttribute('vector-effect', 'non-scaling-stroke');
    if (z.kind === 'ignore') poly.setAttribute('stroke-dasharray', '8 6');
    svg.append(poly);
  }
  return svg;
}

SCREENS.set_cam_zones = (root, p, ctx) => {
  // the picture fits what is left of the screen; the panel with the instruction, the buttons and the zones stays fixed
  // above the menu (it scrolls by itself when it is long) – like playing an event
  const fitArea = el('div', { class: 'fitPlayer zoneFit' });
  const content = el('div', { class: 'content' });
  const panel = el('div', { class: 'under-player details zonePanel' }, content);
  root.append(topBar(tr('Zones'), { back: true }), fitArea, panel);
  (async () => {
    let st, cam;
    try {
      const d = await api('/api/settings');
      st = d.cameras.find((x) => x.id === p.id);
      cam = (await fetchCameras()).find((x) => x.id === p.id);
    } catch (e) { content.append(el('p', { class: 'errorText' }, e.message)); return; }
    if (!st || !cam) { content.append(el('p', { class: 'errorText' }, tr('The camera is not running'))); return; }
    $('.top h1', root).firstChild.textContent = tr('Zones') + ' · ' + cam.name;
    const flip = cam.rotate === 180;
    let zones = JSON.parse(JSON.stringify(st.zones || []));
    let editing = null;
    const img = el('img', { alt: '', draggable: 'false' });
    const layer = el('div', { class: 'zoneLayer' });
    const pic = el('div', { class: 'zonePic', style: rot(cam) }, img, layer);
    const ratio = (cam.width || 4) / (cam.height || 3);
    fitArea.append(pic);
    const resize = () => {
      const W = fitArea.clientWidth - 16, H = fitArea.clientHeight - 16;
      if (W <= 0 || H <= 0) return;
      const w = Math.min(W, H * ratio);
      pic.style.width = Math.floor(w) + 'px'; pic.style.height = Math.floor(w / ratio) + 'px';
    };
    const ro = new ResizeObserver(resize);
    ro.observe(fitArea);
    ctx.cleanup.push(() => ro.disconnect());
    loadImage(img, '/api/cams/' + cam.id + '/snapshot.jpg');
    const hint = el('p', { class: 'hint' });
    const list = el('div', { style: 'display:flex;flex-direction:column;gap:6px' });
    // every change is saved at once (Done and Delete) – there is no separate Save button to forget
    const saveZones = async () => {
      status.className = 'hint'; status.textContent = tr('Saving …');
      try {
        const r = await api('/api/settings/' + p.id, { method: 'PUT', body: { zones: zones.filter((z) => z !== editing).map((z) => ({ id: z.id, name: z.name || '', kind: z.kind, points: z.points, objects: z.kind === 'zone' ? (z.objects || []) : [] })) } });
        const keep = editing;
        zones = (r.zones || []).concat(keep ? [keep] : []);
        status.className = 'ok'; status.textContent = tr('Saved.');
        await fetchCameras();
        return true;
      } catch (e) { status.className = 'errorText'; status.textContent = e.message; return false; }
    };
    const editBox = el('div', { style: 'display:flex;flex-direction:column;gap:8px' });
    const status = el('p', { class: 'hint', role: 'status' });
    const objectsAll = ['person', 'car', 'van', 'bicycle', 'motion'];
    // position of a pointer in the picture (0–1, the camera's own orientation)
    const at = (ev) => {
      const r = pic.getBoundingClientRect();
      let x = Math.min(1, Math.max(0, (ev.clientX - r.left) / r.width)), y = Math.min(1, Math.max(0, (ev.clientY - r.top) / r.height));
      if (flip) { x = 1 - x; y = 1 - y; }
      return [Math.round(x * 1000) / 1000, Math.round(y * 1000) / 1000];
    };
    function draw() {
      layer.replaceChildren(zoneSVG(zones, editing));
      if (editing) {
        editing.points.forEach((pt, i) => {
          const h = el('span', { class: 'zoneHandle', style: 'left:' + (pt[0] * 100) + '%;top:' + (pt[1] * 100) + '%;background:' + zoneColour(zones, editing) });
          h.addEventListener('pointerdown', (ev) => {
            ev.stopPropagation(); ev.preventDefault();
            h.setPointerCapture(ev.pointerId);
            // only the outline and this handle are redrawn while dragging (a new handle would lose the pointer)
            const move = (e2) => {
              const pt2 = at(e2);
              editing.points[i] = pt2;
              h.style.left = (pt2[0] * 100) + '%'; h.style.top = (pt2[1] * 100) + '%';
              const old = layer.querySelector('svg.zones');
              if (old) old.replaceWith(zoneSVG(zones, editing));
            };
            h.addEventListener('pointermove', move);
            h.addEventListener('pointerup', () => { h.removeEventListener('pointermove', move); drawList(); }, { once: true });
          });
          layer.append(h);
        });
      }
      hint.className = 'hint do';
      hint.textContent = !editing ? tr('Add a zone or an area to ignore below. Zones give events their name (e.g. "Person · Driveway"); areas to ignore are left out of motion and AI.')
        : editing.points.length < 3 ? tr('Tap in the picture to set the corners ({n} of at least 3).', { n: editing.points.length })
        : tr('Tap to add corners (at most 8), drag a corner to move it.');
    }
    pic.addEventListener('pointerdown', (ev) => {
      if (!editing || editing.points.length >= 8) return;
      editing.points.push(at(ev));
      draw(); drawList();
    });
    function drawList() {
      const nz = zones.filter((z) => z.kind === 'zone').length, ni = zones.length - nz;
      list.replaceChildren(...zones.map((z) => {
        const row = el('div', { class: 'camRow' + (z === editing ? ' selected' : '') },
          el('span', { class: 'dot', style: 'background:' + zoneColour(zones, z) }),
          el('b', { style: 'flex:1' }, z.kind === 'ignore' ? tr('Area to ignore') : (z.name || tr('New zone'))),
          z === editing ? null : el('button', { type: 'button', class: 'link', onclick: () => { editing = z; draw(); drawList(); drawEdit(); } }, tr('Edit')),
          el('button', { type: 'button', class: 'link', onclick: async () => {
            const saved = z.id && z !== editing;
            if (saved && !confirm(tr('Delete {name}?', { name: z.kind === 'ignore' ? tr('Area to ignore') : z.name }))) return;
            zones = zones.filter((x) => x !== z); if (editing === z) editing = null; draw(); drawList(); drawEdit();
            if (saved) { await saveZones(); draw(); drawList(); }
          } }, tr('Delete')));
        return row;
      }),
      el('div', { class: 'to' },
        el('button', { type: 'button', class: 'btn grey', disabled: nz >= 3 || !!editing, onclick: () => {
          editing = { kind: 'zone', name: '', points: [], objects: [] }; zones.push(editing); draw(); drawList(); drawEdit();
        } }, '+ ' + tr('Zone')),
        el('button', { type: 'button', class: 'btn grey', disabled: ni >= 3 || !!editing, onclick: () => {
          editing = { kind: 'ignore', name: '', points: [] }; zones.push(editing); draw(); drawList(); drawEdit();
        } }, '+ ' + tr('Area to ignore'))));
    }
    function drawEdit() {
      if (!editing) { editBox.replaceChildren(); return; }
      const z = editing;
      const parts = [];
      if (z.kind === 'zone') {
        const name = camInput(z.name, { maxlength: 30, placeholder: tr('e.g. Driveway') });
        name.oninput = () => { z.name = name.value; drawList(); };
        parts.push(field(tr('Name'), name));
        const chips = el('div', { class: 'chips' });
        const drawChips = () => chips.replaceChildren(...objectsAll.map((o) => {
          const on = (z.objects || []).includes(o);
          return el('button', { type: 'button', class: 'chip' + (on ? ' selected' : ''), 'aria-pressed': on ? 'true' : 'false', onclick: () => {
            z.objects = on ? z.objects.filter((x) => x !== o) : [...(z.objects || []), o]; drawChips();
          } }, labelName(o));
        }));
        drawChips();
        parts.push(field(tr('Counts in the zone'), chips, tr('None chosen = all objects.')));
      }
      parts.push(el('div', { class: 'to' },
        el('button', { type: 'button', class: 'btn darkred', onclick: () => { z.points.pop(); draw(); } }, tr('Undo last point')),
        el('button', { type: 'button', class: 'btn green', onclick: async () => {
          if (z.points.length < 3) { status.className = 'errorText'; status.textContent = tr('a zone needs 3–8 points'); return; }
          if (z.kind === 'zone' && !z.name.trim()) { status.className = 'errorText'; status.textContent = tr('give every zone a name'); return; }
          editing = null;
          if (!(await saveZones())) { editing = z; }
          draw(); drawList(); drawEdit();
        } }, tr('Done and save'))));
      editBox.replaceChildren(...parts);
    }
    draw(); drawList(); drawEdit();
    content.append(hint, editBox, status, list);
  })();
};

SCREENS.set_cam_ai = (root, p) => {
  const content = camPage(root, tr('Motion and AI'));
  motionUI(content, p.id);
};

// Motion and AI for one camera (saved with the Save button)
async function motionUI(box, id) {
  let objNote, objHint; // hint under the objects (set below)
  let d;
  try { d = await api('/api/settings'); } catch (e) { box.append(el('p', { class: 'errorText' }, e.message)); return; }
  const selected = d.cameras.find((x) => x.id === id);
  if (!selected) { box.append(el('p', { class: 'hint do' }, tr('The camera is not running – motion and AI can be changed when it is active.'))); return; }
  const status = el('p', { class: 'hint', role: 'status' });
  const send = () => { status.className = 'hint'; status.textContent = tr('Not saved yet.'); };
  const sliderEl = (heading2, fieldName, min, max, step, device, leftText, right) => {
    const inp = el('input', { type: 'range', min, max, step, value: selected[fieldName], 'aria-label': heading2 });
    const tv = el('span', {}, selected[fieldName] + device);
    inp.oninput = () => { selected[fieldName] = +inp.value; tv.textContent = inp.value + device; send(); };
    return el('label', { class: 'slider' }, el('span', { class: 't' }, heading2, tv), inp, el('span', { class: 'e' }, el('span', {}, leftText), el('span', {}, right)));
  };
  box.append(el('div', { class: 'panelBox' },
    sliderEl(tr('Motion sensitivity'), 'sensitivity', 1, 10, 1, ' / 10', tr('Only large movements'), tr('Even small movements')),
    sliderEl(tr('AI confidence'), 'min_confidence', 30, 95, 5, ' %', tr('More events'), tr('Fewer false alarms')),
    toggleButton(tr('Rotate the image 180°'), tr('If the camera is mounted upside down'), selected.rotate === 180, (t) => { selected.rotate = t ? 180 : 0; send(); }),
    toggleButton(tr('No events when I am home'), tr('Controlled by your phone via Home Assistant. Still records 24/7'), !!selected.home_off, (t) => { selected.home_off = t; send(); }),
    sliderEl(tr('Time before the event'), 'pre_seconds', 0, 60, 5, ' s', '0 s', '60 s'),
    sliderEl(tr('Time after the event'), 'post_seconds', 0, 120, 5, ' s', '0 s', '120 s'),
    // "Motion" together with AI objects: every motion becomes an event – the AI then only adds what it saw
    (objNote = el('small', { class: 'hint', style: 'display:block;margin-top:6px' }), objHint = () => {
      const both = selected.objects.includes('motion') && selected.objects.some((o) => o !== 'motion');
      objNote.textContent = both ? tr('With Motion chosen, every motion becomes an event – the AI objects only add what was seen. Choose only the objects to let the AI filter out leaves, light and rain.') : '';
    }, objHint(), null),
    el('div', { class: 'slider' }, el('span', { class: 't' }, tr('Creates an event')),
      el('div', { class: 'obj' }, ...d.objects.map((o) => {
        const b = el('button', { type: 'button', 'aria-pressed': selected.objects.includes(o) ? 'true' : 'false', onclick: () => {
          selected.objects = selected.objects.includes(o) ? selected.objects.filter((x) => x !== o) : selected.objects.concat(o);
          b.setAttribute('aria-pressed', selected.objects.includes(o) ? 'true' : 'false'); objHint(); send();
        } }, labelName(o));
        return b;
      })), objNote)),
  saveButton(tr('Save'), status, async () => {
    const r = await api('/api/settings/' + id, { method: 'PUT', body: { sensitivity: selected.sensitivity, min_confidence: selected.min_confidence, objects: selected.objects, rotate: selected.rotate || 0, pre_seconds: selected.pre_seconds, post_seconds: selected.post_seconds, home_off: !!selected.home_off } });
    const kk = S.cams.find((x) => x.id === id); if (kk) { kk.rotate = r.rotate; kk.pre_seconds = r.pre_seconds; kk.post_seconds = r.post_seconds; }
    Object.assign(selected, r);
  }), status);
}

// ---------- login/logout, update ----------
async function logout(expired) {
  if (!expired && S.token) { try { await api('/api/logout', { method: 'POST', noLogout: true }); } catch (e) { /* */ } }
  S.token = ''; save('token', '');
  NATIVE && NATIVE.login(S.server, '');
  stack.length = 0;
  if (active) active.cleanup.forEach((f) => { try { f(); } catch (e) { /* */ } });
  active = null;
  go('login', {}, true);
}

async function checkVersion(showNone) {
  if (!NATIVE || !S.token) return;
  try {
    const v = await api('/api/version');
    if (v && v.versionCode > NATIVE.versionCode()) updateStatus(tr('New version {v} is ready', { v: v.versionName }), true);
    else if (showNone) updateStatus(tr('You have the latest version'), false, true);
  } catch (e) { if (showNone) updateStatus(tr('Could not check: {f}', { f: e.message }), false, true); }
}
// also called from Android (Opdatering.java)
window.updateStatus = (text, btn, hideSoon) => {
  $('#refresh').hidden = false;
  $('#refreshText').textContent = text;
  $('#refreshBtn').hidden = !btn;
  if (hideSoon) setTimeout(() => { $('#refresh').hidden = true; }, 4000);
};
$('#refreshBtn').onclick = () => { updateStatus(tr('Downloading update …'), false); NATIVE && NATIVE.update(); };
for (const b of document.querySelectorAll('#nav button')) b.onclick = () => navGo(b.dataset.go);
// the bottom menu and the update button are in index.html – translate their texts
$('#nav').setAttribute('aria-label', tr('Main menu'));
for (const n of document.querySelectorAll('#nav button, #refreshBtn')) {
  for (const x of n.childNodes) if (x.nodeType === 3 && x.textContent.trim()) x.textContent = tr(x.textContent.trim());
}

// In Home Assistant's sidebar in a phone browser, Home Assistant makes ArgusSen's frame as tall as the whole screen –
// also the part that the browser's address bar covers. Then the bottom menu lies below the visible area. The frame
// has the same origin as Home Assistant, so ArgusSen measures how much of it is visible and fits itself to that.
function fitToVisibleFrame() {
  const app = document.getElementById('app');
  try {
    const frame = window.frameElement;
    const parent = window.parent;
    if (!frame || !parent || parent === window) return;
    const vv = parent.visualViewport;
    const visibleBottom = vv ? vv.offsetTop + vv.height : parent.innerHeight;
    const r = frame.getBoundingClientRect();
    const visible = Math.floor(visibleBottom - Math.max(r.top, 0));
    app.style.height = visible > 200 && visible < r.height - 1 ? visible + 'px' : '';
  } catch (e) { app.style.height = ''; } // another origin: nothing to measure
}
fitToVisibleFrame();
try {
  if (window.frameElement && window.parent !== window) {
    const pv = window.parent.visualViewport || window.parent;
    pv.addEventListener('resize', fitToVisibleFrame);
    pv.addEventListener('scroll', fitToVisibleFrame);
    window.parent.addEventListener('resize', fitToVisibleFrame);
  }
} catch (e) { /* another origin */ }
window.addEventListener('resize', fitToVisibleFrame);

// When the app is in the background, video and image refresh stop
window.appPause = () => { if (active) { const { name, p } = active; active.cleanup.forEach((f) => { try { f(); } catch (e) { /* */ } }); active = { name, p, cleanup: [], asleep: true }; } };
window.appResume = () => { if (active && active.asleep) go(active.name, active.p, true); checkVersion(false); };

// Direct links (e.g. from Home Assistant): #camera/door, #event/123, #events, #status
function openLink() {
  // older links used Danish words (from Home Assistant dashboards and the Android app) – they still work
  const ALIASES = { kamera: 'camera', haendelse: 'event', 'hændelse': 'event', seneste: 'latest', haendelser: 'events' };
  let [what, id] = decodeURIComponent(location.hash.replace(/^#/, '')).split('/');
  if (!what) return;
  what = ALIASES[what] || what;
  history.replaceState(null, '', location.pathname);
  if (what === 'camera' && S.cams.some((c) => c.id === id)) go('camera', { cam: id });
  else if (what === 'event' && +id) go('playback', { id: +id });
  else if (what === 'latest' && S.cams.some((c) => c.id === id)) {
    api('/api/events?limit=1&cam=' + encodeURIComponent(id)).then((l) => { if (l.length) go('playback', { id: l[0].id, ev: l[0] }); else go('camera', { cam: id }); }).catch(() => {});
  }
  else if (what === 'events' || what === 'status' || what === 'live') navGo(what);
}
window.addEventListener('hashchange', () => { if (S.token || S.ha) openLink(); });

// As a Home Assistant app the user is already logged in to HA (ingress) – so no token is needed.
async function haLogin() {
  // only in Home Assistant's sidebar (ingress) – elsewhere it would only give a refused request on every start
  if (!WEB || !/\/api\/hassio_ingress\//.test(location.pathname)) return false;
  try {
    const r = await fetch(S.server + '/api/me', { cache: 'no-store' });
    if (!r.ok) return false;
    S.user = (await r.json()).user; S.ha = true;
    return true;
  } catch (e) { return false; }
}

// setupNeeded: a standalone ArgusSen without any users yet (the web version only).
async function setupNeeded() {
  if (!WEB) return false;
  try {
    const r = await fetch(S.server + '/api/setup', { cache: 'no-store' });
    return r.ok && (await r.json()).needed === true;
  } catch (e) { return false; }
}

// syncLanguage: remembers ArgusSen's language; if this device follows it and it has changed, the page is reloaded.
async function syncLanguage() {
  try {
    const l = await api('/api/language');
    if (!l || !LANG_NAMES[l.language]) return false;
    save('serverLanguage', l.language);
    if (load('language', 'auto') === 'auto' && l.language !== LANG) {
      let tried = ''; try { tried = sessionStorage.getItem('argus.langReload') || ''; } catch (e) { /* */ }
      if (tried !== l.language) {
        try { sessionStorage.setItem('argus.langReload', l.language); } catch (e) { /* */ }
        location.reload();
        return true;
      }
    }
  } catch (e) { /* older server without /api/language */ }
  return false;
}

async function start() {
  if (!S.token && !(await haLogin())) { go((await setupNeeded()) ? 'setup' : 'login', {}, true); return; }
  NATIVE && NATIVE.login(S.server, S.token);
  try { const me = await api('/api/me'); S.user = me.user; S.standalone = !!me.standalone; save('user', S.user); } catch (e) { if (e instanceof AuthError) return; }
  if (await syncLanguage()) return;
  await fetchCameras();
  go('overview', {}, true);
  openLink();
  fetchEvents();
  checkVersion(false);
}
setInterval(() => checkVersion(false), 30 * 60000);
start();
