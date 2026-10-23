'use strict';
// Kotib Mini App. Xavfsizlik: barcha dinamik matn textContent orqali qo'yiladi
// (innerHTML ishlatilmaydi) — kontakt xabarlari XSS bera olmaydi. Inline skript/stil yo'q (qat'iy CSP).

const tg = window.Telegram && window.Telegram.WebApp;
const root = document.getElementById('app');
const nav = document.getElementById('nav');
const toastEl = document.getElementById('toast');

const PERSONA_LABELS = {
  neutral: "Do'stona (standart)", joke: 'Hazil ruhida', son: "O'g'il nomidan",
  formal: 'Rasmiy', brief: 'Juda qisqa', mine: "🪞 O'zimdek (o'rganilgan)", custom: "O'z ko'rsatmam",
};
const TIMEZONES = [
  ['Asia/Tashkent', 'Toshkent (UTC+5)'], ['Asia/Samarkand', 'Samarqand (UTC+5)'], ['Asia/Almaty', 'Olmaota (UTC+5)'],
  ['Asia/Bishkek', 'Bishkek (UTC+6)'], ['Asia/Dushanbe', 'Dushanbe (UTC+5)'], ['Europe/Moscow', 'Moskva (UTC+3)'],
  ['Europe/Istanbul', 'Istanbul (UTC+3)'], ['Asia/Dubai', 'Dubay (UTC+4)'], ['Asia/Seoul', 'Seul (UTC+9)'],
  ['Europe/Berlin', 'Berlin'], ['Europe/London', 'London'], ['America/New_York', 'Nyu-York'],
];
const EVENTS = {
  consent: 'Rozilik berildi', login_begin: 'Ulanish boshlandi', login_ok: 'Akkaunt ulandi',
  login_failed: 'Ulanish muvaffaqiyatsiz', settings_saved: 'Sozlamalar saqlandi', paused: "To'xtatildi",
  resumed: 'Davom ettirildi', draft_created: 'Qoralama tayyorlandi', draft_sent: 'Qoralama yuborildi',
  draft_rejected: 'Qoralama rad etildi', auto_sent: 'Avtomatik yuborildi', skip: "O'tkazib yuborildi",
  blocked: 'Xavfsizlik filtri ishladi', output_rejected: "Javob filtrdan o'tmadi", llm_error: 'AI javob bermadi',
  flood_wait: "Telegram kutishni so'radi", fatal: 'Telegram xatosi', circuit_breaker: "Xavfsizlik uchun to'xtatildi",
  trial_started: 'Bepul sinov boshlandi', payment: "To'lov qabul qilindi", subscription_canceled: 'Obuna bekor qilindi',
  subscription_renewed: 'Obuna qayta yoqildi', refunded: "To'lov qaytarildi",
  voice_error: "Ovozli xabar yuklanmadi", digest_sent: 'Kechki xulosa yuborildi', followup_reminded: "Va'da eslatildi", hook: 'Avtomatika rejimni o\'zgartirdi', hook_created: 'Avtomatlashtirish havolasi yaratildi', hook_deleted: "Avtomatlashtirish havolasi o'chirildi",
  google_connected: 'Google ulandi', google_disconnected: 'Google uzildi', google_revoked: "Google ruxsati bekor qilindi",
  action_proposed: 'Taklif yuborildi (taqvim/vazifa)', action_done: "Google'ga qo'shildi",
  action_rejected: 'Taklif rad etildi', action_failed: "Google'ga qo'shib bo'lmadi",
  referral_bonus: "Do'st taklifi uchun bonus kunlar", account_linked: "Qo'shimcha akkaunt rozilik berdi",
  account_unlinked: "Qo'shimcha akkaunt uzildi", weekly_sent: 'Haftalik hisobot yuborildi', link_created: 'Akkaunt ulash havolasi yaratildi', away_set: "Holat o'zgartirildi",
  urgent_alert: 'Shoshilinch xabar bildirildi', voice_transcribed: "Ovozli xabar matnga o'girildi", photo_error: 'Rasm yuklanmadi',
  unanswered_reminded: 'Javobsiz chatlar eslatildi', summary_made: 'AI xulosa tayyorlandi', morning_sent: 'Ertalabki brifing yuborildi',
  shadow_prepared: 'Soya rejimi: javob jim tayyorlandi', shadow_set: "Soya rejimi o'zgartirildi", shadow_report: 'Soya rejimi natijasi yuborildi',
  style_learn_started: "Uslub o'rganish boshlandi", style_learned: 'Uslub profili yangilandi', style_refine_run: "Uslub o'qitish tekshirildi",
  birthdays_checked: "Tug'ilgan kunlar tekshirildi", birthday_draft: "Tug'ilgan kun tabrigi tayyorlandi", ask_answered: '/sor savoliga javob berildi',
  schedule_created: 'Rejalashtirilgan vazifa yaratildi', schedule_updated: "Vazifa o'zgartirildi", schedule_deleted: "Vazifa o'chirildi",
  schedule_run: 'Rejalashtirilgan vazifa bajarildi', plugin_enabled: 'Plagin yoqildi', plugin_disabled: "Plagin o'chirildi", plugin_removed: 'Plagin uzildi', plugin_connected: 'Plagin akkaunti ulandi',
  charity_donation: '💚 Hayriyaga xayriya qilindi', charity_paid: "💚 Hayriya o'tkazmasi belgilandi", charity_manual: "💚 Qo'shimcha xayriya yozildi",
};

const state = {
  me: null, settings: null, contacts: null, drafts: null, activity: null,
  tab: 'home', login: { step: 'idle' }, dirty: false,
};

// ---------- yordamchilar ----------
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'value') el.value = v;
    else if (v === true) el.setAttribute(k, '');
    else if (v !== false && v != null) el.setAttribute(k, v);
  }
  for (const kid of kids.flat()) if (kid != null && kid !== false) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  return el;
}

const SVG_NS = 'http://www.w3.org/2000/svg';
const ICONS = {
  home: 'M3 10.5 12 3l9 7.5V20a1 1 0 0 1-1 1h-5v-6H9v6H4a1 1 0 0 1-1-1z',
  inbox: 'M4 4h16v10h-4.5l-1.5 2.5h-4L8.5 14H4zM4 14v6h16v-6',
  rules: 'M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01',
  settings: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z',
  user: 'M20 21a8 8 0 0 0-16 0M12 13a5 5 0 1 0 0-10 5 5 0 0 0 0 10z',
  clock: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 2',
};
function icon(name) {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.8');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  const p = document.createElementNS(SVG_NS, 'path');
  p.setAttribute('d', ICONS[name]);
  svg.append(p);
  return svg;
}

async function api(method, path, body) {
  const headers = { 'Authorization': 'tma ' + (tg ? tg.initData : ''), 'Content-Type': 'application/json' };
  if (state.account) headers['X-Kotib-Account'] = String(state.account); // tanlangan qo'shimcha akkaunt
  const res = await fetch('/api' + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    cache: 'no-store',
  });
  let data = null;
  try { data = await res.json(); } catch (_) { /* bo'sh javob */ }
  if (!res.ok) {
    const err = new Error((data && data.error) || ('Xatolik ' + res.status));
    err.status = res.status;
    throw err;
  }
  return data;
}

function haptic(kind) {
  try {
    if (!tg || !tg.HapticFeedback) return;
    if (kind === 'tap') tg.HapticFeedback.selectionChanged();
    else tg.HapticFeedback.notificationOccurred(kind);
  } catch (_) { /* eski klient */ }
}

let toastTimer = 0;
function toast(text, bad) {
  toastEl.textContent = text;
  toastEl.className = 'show' + (bad ? ' bad' : '');
  haptic(bad ? 'error' : 'success');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { toastEl.className = ''; }, 2600);
}
function fail(e) { toast(e && e.message ? e.message : 'Xatolik', true); }

function confirmBox(text) {
  return new Promise((resolve) => {
    if (tg && tg.showConfirm) {
      try { tg.showConfirm(text, resolve); return; } catch (_) { /* eski klient */ }
    }
    resolve(window.confirm(text));
  });
}

// busy: tugmani bosilganda bloklaydi (ikki marta yuborishdan himoya).
function busy(fn) {
  return async (ev) => {
    const b = ev && ev.currentTarget;
    if (b) b.disabled = true;
    try { await fn(ev); } catch (e) { fail(e); } finally { if (b && b.isConnected) b.disabled = false; }
  };
}

// put — el.append, lekin null/false'ni tashlab yuboradi (DOM append ularni "null" matniga aylantiradi).
function put(el, ...kids) { el.append(...kids.flat().filter((k) => k != null && k !== false)); }
function swap(el, ...kids) { el.replaceChildren(...kids.flat().filter((k) => k != null && k !== false)); }

const rid = () => 'r' + Math.random().toString(36).slice(2, 10);
function fmtDate(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  return d.toLocaleDateString('uz-UZ', { day: '2-digit', month: '2-digit', year: 'numeric' });
}
function fmtAgo(iso) {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return 'hozirgina';
  if (s < 3600) return Math.floor(s / 60) + ' daqiqa oldin';
  if (s < 86400) return Math.floor(s / 3600) + ' soat oldin';
  if (s < 7 * 86400) return Math.floor(s / 86400) + ' kun oldin';
  return fmtDate(iso);
}

// ---------- tinch soatlar ----------
function hmToMin(v) { const m = /^(\d{2}):(\d{2})$/.exec(v || ''); return m ? (+m[1]) * 60 + (+m[2]) : null; }
// quietInfo: oraliq davomiyligi (soat) va hozir shu oraliqdami (tanlangan mintaqa bo'yicha).
function quietInfo(q) {
  const a = hmToMin(q.start), b = hmToMin(q.end);
  if (!q.enabled || a == null || b == null || a === b) return null;
  const hours = Math.round(((b - a + 1440) % 1440) / 6) / 10;
  let now = null;
  try {
    const t = new Intl.DateTimeFormat('en-GB', { timeZone: q.tz, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date());
    now = hmToMin(t);
  } catch (_) { /* noma'lum mintaqa */ }
  const active = now != null && (a < b ? now >= a && now < b : now >= a || now < b);
  return { hours, active };
}

// ---------- Face ID / barmoq izi ----------
// Yoqilgan bo'lsa: Mini App'dan qoralama yuborish, avtomatik rejimni yoqish, Google'ni uzish va
// akkauntni o'chirishdan oldin biometrik tasdiq so'raladi. Tanlov shu qurilmada saqlanadi.
const BIO_KEY = 'kotib.bio';
const bio = { ready: false, available: false, type: 'unknown' };
function bioMgr() { return tg && tg.BiometricManager; }
function bioInit() {
  const m = bioMgr();
  if (!m) return;
  try {
    m.init(() => { bio.ready = true; bio.available = !!m.isBiometricAvailable; bio.type = m.biometricType || 'unknown'; if (state.tab === 'account' && state.me) renderMain(); });
  } catch (_) { /* eski klient */ }
}
function bioName() { return bio.type === 'face' ? 'Face ID' : bio.type === 'finger' ? 'Barmoq izi' : 'Biometrik himoya'; }
function bioEnabled() {
  try { return localStorage.getItem(BIO_KEY) === '1'; } catch (_) { return false; }
}
function bioSet(on) { try { if (on) localStorage.setItem(BIO_KEY, '1'); else localStorage.removeItem(BIO_KEY); } catch (_) { /* */ } }
function bioRequestAccess() {
  return new Promise((resolve) => {
    const m = bioMgr();
    if (!m || !bio.available) return resolve(false);
    if (m.isAccessGranted) return resolve(true);
    try { m.requestAccess({ reason: "Muhim amallarni tasdiqlash uchun" }, (ok) => resolve(!!ok)); } catch (_) { resolve(false); }
  });
}
// bioGate — himoya yoqilgan bo'lsa biometrik tasdiq; rad etilsa xato (busy() toast ko'rsatadi).
function bioGate(reason) {
  return new Promise((resolve, reject) => {
    const m = bioMgr();
    if (!bioEnabled() || !m || !bio.available) return resolve();
    if (!m.isAccessGranted) { bioSet(false); return resolve(); } // ruxsat Telegram sozlamalarida olib tashlangan
    try {
      m.authenticate({ reason }, (ok) => (ok ? resolve() : reject(new Error('Tasdiqlanmadi'))));
    } catch (_) { resolve(); }
  });
}

// ---------- mavzu ----------
function applyTheme() {
  const dark = tg ? tg.colorScheme === 'dark' : window.matchMedia('(prefers-color-scheme: dark)').matches;
  document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light');
  const bg = dark ? '#262624' : '#faf9f5';
  document.querySelector('meta[name=theme-color]').setAttribute('content', bg);
  try { if (tg) { tg.setHeaderColor(bg); tg.setBackgroundColor(bg); if (tg.setBottomBarColor) tg.setBottomBarColor(bg); } } catch (_) { /* eski klient */ }
}

// ---------- ishga tushish ----------
async function boot() {
  applyTheme();
  if (tg) {
    tg.ready(); tg.expand();
    try { tg.onEvent('themeChanged', applyTheme); } catch (_) { /* */ }
    // Matn yozayotganda pastga surilsa ilova yopilib, yozilgan matn yo'qolmasin (ayniqsa iPhone'da).
    try { if (tg.disableVerticalSwipes) tg.disableVerticalSwipes(); } catch (_) { /* eski klient */ }
    // Telegram "orqaga" tugmasi (Android'da tizim tugmasi ham shu): bo'limdan Asosiy'ga qaytaradi.
    try { if (tg.BackButton) tg.BackButton.onClick(onBack); } catch (_) { /* */ }
    bioInit();
  }
  if (!tg || !tg.initData) {
    swap(root, h('div', { class: 'empty' },
      h('img', { class: 'logo', src: 'logo.svg', alt: 'Kotib', width: 56, height: 56 }),
      h('div', { class: 'big' }, 'Ilovani Telegram ichidan oching'),
      h('p', { class: 'muted' }, 'Kotib faqat Telegram bot orqali ishlaydi: botga /start yozing va «Ochish» tugmasini bosing.'),
      h('p', { class: 'small' }, legalLink('/privacy', 'Maxfiylik siyosati'), ' · ', legalLink('/terms', 'Foydalanish shartlari'))));
    return;
  }
  try { state.account = +localStorage.getItem(ACC_KEY) || null; } catch (_) { /* */ }
  try {
    try { await loadAccountData(); } catch (e) {
      if (!state.account) throw e;
      state.account = null; saveAccount(); // tanlangan akkaunt uzilgan bo'lishi mumkin — asosiyga qaytamiz
      await loadAccountData();
    }
  } catch (e) {
    swap(root, h('div', { class: 'empty' },
      h('div', { class: 'big' }, 'Ochilmadi'),
      h('p', { class: 'err' }, e.message),
      h('button', { class: 'btn', onclick: () => location.reload() }, 'Qayta urinish')));
    return;
  }
  if (state.me.banned) { render(); return; }
  if (state.me.consented && needsConnect()) await resumeLogin();
  render();
  setInterval(backgroundRefresh, 20000);
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) return;
    backgroundRefresh();
    if (state.sub === viewPlugins) renderMain(); // brauzerda akkaunt ulangach — holat yangilansin
  });
}

async function refreshMe() { state.me = await api('GET', '/me'); }

// ---------- Bir nechta akkaunt ----------
const ACC_KEY = 'kotib.account';
function saveAccount() { try { if (state.account) localStorage.setItem(ACC_KEY, String(state.account)); else localStorage.removeItem(ACC_KEY); } catch (_) { /* */ } }
function isChild() { return !!state.me && state.me.account_id !== state.me.main_id; }
function accountLabel() {
  const a = (state.me.accounts || []).find((x) => x.id === state.me.account_id);
  return a ? a.label : '';
}

async function loadAccountData() {
  await refreshMe();
  if (state.me.banned) return; // bloklangan: faqat holat ko'rsatiladi (boshqa API yopiq)
  state.settings = await api('GET', '/settings');
  if (!Array.isArray(state.settings.rules)) state.settings.rules = [];
  state.savedMode = state.settings.mode;
  if (!state.settings.quiet_hours) state.settings.quiet_hours = { enabled: false, start: '23:00', end: '07:00', tz: 'Asia/Tashkent' };
}

async function switchAccount(id) {
  state.account = id === state.me.main_id ? null : id;
  saveAccount();
  Object.assign(state, { login: { step: 'idle' }, drafts: null, contacts: null, activity: null, dirty: false, hookURL: null, tab: 'home' });
  await loadAccountData();
  if (state.me.consented && needsConnect()) await resumeLogin();
  haptic('success');
  render();
}

// Akkaunt almashtirgich (2+ akkaunt bo'lsa).
function accountBar() {
  const list = state.me.accounts || [];
  if (list.length < 2) return null;
  return h('div', { class: 'acc-bar' }, list.map((a) => {
    const on = a.id === state.me.account_id;
    const dot = a.status === 'active' ? 'ok' : a.status === 'paused' ? 'warn' : 'bad';
    return h('button', { class: 'chip-btn' + (on ? ' on' : ''), onclick: on ? null : busy(() => switchAccount(a.id)) },
      h('i', { class: 'dot ' + dot }), (a.main ? '🏠 ' : '👤 ') + a.label);
  }));
}

function accountsCard() {
  if (needsPro()) {
    return proLock("Bir nechta akkaunt", "Ish, do'kon yoki boshqa Telegram akkauntlaringizni ham ulang — hammasi bitta joydan boshqariladi, qoralamalar shu botga keladi.");
  }
  const card = h('div', { class: 'card' });
  let linkRes = null, adding = false;
  const draw = async () => {
    let info;
    try { info = await api('GET', '/accounts'); } catch (e) { swap(card, h('p', { class: 'err small' }, e.message)); return; }
    const kids = [h('div', { class: 'card-head' }, h('h3', {}, 'Akkauntlar'), h('span', { class: 'badge' }, `${info.accounts.length - 1}/${info.max} qo'shimcha`))];
    kids.push(h('p', { class: 'muted small' }, "Boshqa Telegram akkauntlaringiz (ish, do'kon) ham kotibga ulanadi. Qoralamalar va xabarlar shu botga «👤 nom» bilan keladi."));
    kids.push(h('ul', { class: 'acc-list' }, info.accounts.map((a) => h('li', {},
      h('div', {}, h('b', {}, (a.main ? '🏠 ' : '👤 ') + a.label), h('div', { class: 'muted small' }, (STATUS[a.status] || [a.status])[0])),
      a.main ? null : h('div', { class: 'row' },
        h('button', { class: 'btn ghost sm', onclick: busy(() => switchAccount(a.id)) }, a.status === 'pending' ? 'Ulash' : 'Ochish'),
        h('button', { class: 'btn ghost sm', onclick: busy(async () => {
          if (!(await confirmBox(`«${a.label}» uziladi: Telegram sessiyasi tugatiladi, uning sozlama va qoralamalari o'chadi. Davom etasizmi?`))) return;
          await api('DELETE', '/accounts/' + a.id); await refreshMe(); toast('Uzildi'); draw();
        }) }, 'Uzish'))))));
    if (linkRes) {
      kids.push(h('div', { class: 'card accent' },
        h('p', { class: 'small' }, h('b', {}, '2-qadam: '), "havolani QO'SHILADIGAN akkauntdan oching va botda «Roziman»ni bosing (30 daqiqa amal qiladi)."),
        h('ol', { class: 'small muted' },
          h('li', {}, "Havoladan nusxa oling."),
          h('li', {}, "Telegram'da qo'shiladigan akkauntga o'ting (Sozlamalar → akkaunt almashtirish)."),
          h('li', {}, "Havolani o'sha akkauntning «Saqlangan xabarlar»iga yuborib, bosing → «Roziman»."),
          h('li', {}, "Shu yerga qayting: akkaunt ro'yxatda paydo bo'ladi — «Ulash»ni bosing.")),
        h('div', { class: 'hook-list' },
          h('button', { class: 'btn block', onclick: () => copyText(linkRes.link) }, 'Havoladan nusxa olish'),
          h('button', { class: 'btn ghost block', onclick: busy(async () => { await refreshMe(); linkRes = null; draw(); }) }, "Tayyor — ro'yxatni yangilash"))));
    } else if (adding) {
      const label = h('input', { type: 'text', maxlength: 32, placeholder: "Nomi, masalan: Ish yoki Do'kon" });
      kids.push(h('div', { class: 'stack' }, label,
        h('button', { class: 'btn block', onclick: busy(async () => {
          linkRes = await api('POST', '/accounts/link', { label: label.value }); adding = false; draw();
        }) }, 'Havola olish')));
      setTimeout(() => label.focus(), 50);
    } else if (info.can_add) {
      kids.push(h('button', { class: 'btn block', onclick: () => { adding = true; draw(); } }, '+ Akkaunt qo\'shish'));
    }
    swap(card, kids);
  };
  put(card, h('div', { class: 'skeleton' }));
  draw();
  return card;
}

async function backgroundRefresh() {
  if (document.hidden || !state.me || !state.me.consented || needsConnect()) return;
  try {
    const before = state.me.pending_drafts;
    await refreshMe();
    renderNav();
    if (state.tab === 'drafts' && state.me.pending_drafts !== before) renderMain();
    if (state.tab === 'home') renderMain();
  } catch (_) { /* tarmoq: keyingi safar */ }
}

function needsConnect() { return state.me.status === 'pending' || state.me.status === 'revoked'; }

// resumeLogin — Mini App qayta ochilganda serverda davom etayotgan ulanishni (kod/parol) tiklaydi.
async function resumeLogin() {
  try {
    const st = await api('GET', '/login/state');
    if (st && ['wait_code', 'wait_password', 'connecting'].includes(st.step)) state.login = st;
  } catch (_) { /* yo'q bo'lsa — oddiy shakl */ }
}

// onBack — Telegram «orqaga» tugmasi (Android'da tizim tugmasi ham): ulanish jarayonida —
// raqam kiritishga qaytadi, ochiq ichki sahifada — uni yopadi, aks holda Asosiy bo'limga.
function onBack() {
  if (state.me && state.me.consented && needsConnect()) {
    if (state.login.step !== 'idle') cancelLogin();
    return;
  }
  if (state.sub) { state.sub = null; haptic('tap'); renderMain(); return; }
  go('home');
}

// cancelLogin — jarayondagi ulanishni bekor qilib, telefon raqam sahifasiga qaytaradi.
async function cancelLogin() {
  haptic('tap');
  state.login = { step: 'idle' };
  renderConnect();
  try { await api('POST', '/login/cancel'); } catch (_) { /* tarmoq: server 5 daqiqada o'zi tugatadi */ }
}

function render() {
  if (state.me.banned) return renderBanned();
  if (!state.me.consented) return renderConsent();
  if (needsConnect()) {
    renderConnect();
    if (isChild()) {
      root.prepend(h('div', { class: 'card accent' },
        h('p', { class: 'small' }, h('b', {}, `👤 «${accountLabel()}» akkauntini ulayapsiz. `),
          "Shu akkauntning telefon raqamini kiriting — kod o'sha akkauntga keladi. Boshqa akkaunt raqami kiritilsa, ulanish bekor qilinadi."),
        h('button', { class: 'btn ghost sm', onclick: busy(() => switchAccount(state.me.main_id)) }, '← Asosiy akkauntga qaytish')));
    }
    return;
  }
  renderMain();
}

function renderBanned() {
  nav.hidden = true;
  const ban = state.me.banned;
  swap(root, h('div', { class: 'empty' },
    h('img', { class: 'logo', src: 'logo.svg', alt: 'Kotib', width: 56, height: 56 }),
    h('div', { class: 'big' }, '⛔ Akkaunt bloklangan'),
    h('p', { class: 'muted' }, "Administrator akkauntingizni blokladi — kotib ishlamaydi."),
    ban.reason ? h('p', { class: 'small' }, 'Sabab: ' + ban.reason) : null,
    h('p', { class: 'muted small' }, "Xato deb hisoblasangiz, administrator bilan bog'laning."),
    h('p', { class: 'small' }, legalLink('/terms', 'Foydalanish shartlari'))));
}

// legalLink — huquqiy sahifa havolasi: Telegram ichida tashqi brauzerda ochiladi.
function legalLink(path, label) {
  return h('a', { href: path, target: '_blank', rel: 'noopener', onclick: (e) => {
    if (tg && tg.openLink) { e.preventDefault(); tg.openLink(location.origin + path); }
  } }, label);
}

// ---------- rozilik ----------
function steps(n) { return h('div', { class: 'steps' }, [1, 2, 3].map((i) => h('i', { class: i <= n ? 'on' : '' }))); }

function renderConsent() {
  nav.hidden = true;
  const b = state.me.billing || {};
  const agree = h('input', { type: 'checkbox' });
  const go = h('button', { class: 'btn block', disabled: true, onclick: busy(async () => {
    await api('POST', '/consent', { accepted: true });
    await refreshMe(); haptic('success'); render();
  }) }, 'Boshlash');
  agree.addEventListener('change', () => { go.disabled = !agree.checked; haptic('tap'); });

  swap(root, 
    steps(1),
    h('img', { class: 'logo', src: 'logo.svg', alt: 'Kotib', width: 56, height: 56 }),
    h('div', { class: 'hero' },
      h('h1', {}, 'Kotibingiz bilan tanishing'),
      h('p', { class: 'muted' }, "Kiruvchi shaxsiy xabarlaringizga sizning uslubingizda javob qoralamasini tayyorlaydi. Siz tasdiqlamaguncha hech narsa yuborilmaydi.")),
    h('div', { class: 'card' },
      h('ul', { class: 'perks' },
        h('li', {}, 'Standart rejim — qoralama: har bir javobni o\'zingiz ko\'rib yuborasiz'),
        h('li', {}, 'Faqat kontaktlaringizga; guruh va kanallarga javob yo\'q, botlarga — standart holatda yo\'q'),
        h('li', {}, 'Pul, parol, hujjat kabi sezgir mavzular hech qachon avtomatik yuborilmaydi'),
        h('li', {}, 'Sessiya shifrlangan; istalgan payt uzib, hammasini o\'chirishingiz mumkin')),
      b.enabled ? h('p', { class: 'muted' }, `Birinchi ${b.trial_days} kun bepul${b.trial_daily_limit ? ` (kuniga ${b.trial_daily_limit} tagacha javob)` : ''}, keyin oyiga ${b.price} ⭐ (Telegram Stars).`) : null),
    h('div', { class: 'card soft' },
      h('p', { class: 'small muted' }, "Muhim: Telegram avtomatik javoblarni cheklashi mumkin — akkauntingiz cheklanish xavfi doim mavjud. Suhbatdoshlaringizning xabarlari javob tayyorlash uchun AI xizmatiga (Google Gemini) yuboriladi. Javoblar siz nomidan ketadi — mas'uliyat sizda."),
      h('label', { class: 'row nowrap' }, agree, h('span', { class: 'small' }, 'Tushundim va ', legalLink('/terms', 'Foydalanish shartlari'), ' hamda ', legalLink('/privacy', 'Maxfiylik siyosati'), 'ga roziman'))),
    go);
}

// ---------- akkauntni ulash ----------
// codeWhere — Telegram kodni qayerga yuborganini tushuntiradi (eng ko'p savol: "kod kelmadi").
function codeWhere(st) {
  switch (st.via) {
    case 'app':
      return "Kod Telegram ilovangizga — «Telegram» nomli rasmiy chatga (ko'k belgili) keldi. Bildirishnoma chiqmasligi mumkin: chatlar ro'yxatidan «Telegram»ni oching. Boshqa qurilmangizdagi (kompyuter, planshet) Telegram'ni ham tekshiring.";
    case 'sms':
      return 'Kod SMS orqali telefon raqamingizga yuborildi.';
    case 'call':
      return "Telegram qo'ng'iroq qiladi — kodni qo'ng'iroqdan (yoki qo'ng'iroq qilgan raqamning oxirgi raqamlaridan) oling.";
    case 'email':
      return `Kod login email manzilingizga yuborildi${st.hint ? ' (' + st.hint + ')' : ''}. Pochtangizni, «Spam» papkasini ham tekshiring.`;
    case 'fragment':
      return "Raqamingiz Fragment orqali olingan — kod fragment.com'dagi hisobingizga keladi.";
    default:
      return "Telegram ilovangizdagi «Telegram» rasmiy chatiga kelgan kodni yozing.";
  }
}

function renderConnect() {
  nav.hidden = true;
  const st = state.login;
  const back = (label) => h('button', { class: 'btn ghost block', onclick: cancelLogin }, label || "← Raqamni o'zgartirish");
  let body;
  if (st.step === 'wait_code') {
    const code = h('input', { type: 'tel', inputmode: 'numeric', autocomplete: 'one-time-code', placeholder: '12345', maxlength: 8 });
    const submit = busy(() => submitLogin('/login/code', { code: code.value }));
    code.addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(e); });
    body = [h('h3', {}, 'Kodni kiriting'),
      h('p', { class: 'muted' }, codeWhere(st)), code,
      h('div', { class: 'card accent' }, h('p', { class: 'small' }, "Kodni hech kimga yubormang va chatga yozmang — Telegram kirishni bloklashi mumkin.")),
      h('button', { class: 'btn block', onclick: submit }, 'Tasdiqlash'),
      back("← Kod kelmadi / raqamni o'zgartirish")];
    setTimeout(() => code.focus(), 50);
  } else if (st.step === 'wait_password') {
    const pw = h('input', { type: 'password', placeholder: 'Ikki bosqichli parol', autocomplete: 'current-password' });
    body = [h('h3', {}, 'Ikki bosqichli parol'),
      h('p', { class: 'muted' }, 'Akkauntingizda 2FA yoqilgan. Parol saqlanmaydi — faqat bir marta ishlatiladi.'), pw,
      h('button', { class: 'btn block', onclick: busy(() => submitLogin('/login/password', { password: pw.value })) }, 'Tasdiqlash'),
      back('← Bekor qilish')];
  } else if (st.step === 'connecting') {
    body = [h('h3', {}, 'Ulanmoqda…'), h('div', { class: 'skeleton' }), back('← Bekor qilish')];
    setTimeout(pollLogin, 1500);
  } else if (st.step === 'done') {
    body = [h('h3', {}, 'Ulandi ✓'), h('p', { class: 'muted' }, 'Hammasi tayyor. Endi kotibni sozlang.'),
      h('button', { class: 'btn block', onclick: busy(async () => { await refreshMe(); state.tab = 'home'; render(); }) }, 'Davom etish')];
  } else {
    // idle yoki error: telefon raqam shakli (xato bo'lsa — pastda ko'rsatiladi).
    const phone = h('input', { type: 'tel', placeholder: '+998 90 123 45 67', autocomplete: 'tel', value: state.lastPhone || '' });
    const dead = state.me.status === 'revoked' && state.me.note;
    const start = busy(async () => {
      state.lastPhone = phone.value;
      await api('POST', '/login/phone', { phone: phone.value });
      state.login = { step: 'connecting' }; render();
    });
    phone.addEventListener('keydown', (e) => { if (e.key === 'Enter') start(e); });
    body = [h('h3', {}, dead ? 'Akkauntni qayta ulang' : 'Telegram akkauntini ulang'),
      dead ? h('div', { class: 'card accent' }, h('p', { class: 'small' }, state.me.note + '. Bu sessiya endi ishlamaydi — qayta kiring. Qoidalar va sozlamalaringiz saqlangan.')) : null,
      h('p', { class: 'muted' }, "Telegram'dagi telefon raqamingizni kiriting — Telegram tasdiqlash kodini SMS yoki Telegram ilovangizga yuboradi. Kod faqat shu oynaga kiritiladi."),
      phone,
      h('button', { class: 'btn block', onclick: start }, 'Kod olish')];
    setTimeout(() => phone.focus(), 50);
  }
  try { if (tg && tg.BackButton) { if (st.step === 'idle' || st.step === 'error' || st.step === 'done') tg.BackButton.hide(); else tg.BackButton.show(); } } catch (_) { /* */ }
  swap(root, steps(2),
    h('div', { class: 'card stack' }, body),
    st.error ? h('p', { class: 'err' }, st.error) : null,
    h('p', { class: 'muted small center' }, "Telegram → Sozlamalar → Qurilmalar bo'limida kotib sessiyasini istalgan payt uzishingiz mumkin."));
}

async function submitLogin(path, payload) {
  await api('POST', path, payload);
  state.login = { step: 'connecting' };
  render();
}

async function pollLogin() {
  if (state.login.step !== 'connecting') return; // «Bekor qilish» bosilgan
  try {
    const st = await api('GET', '/login/state');
    if (state.login.step !== 'connecting') return;
    state.login = st;
    if (st.step === 'done') await refreshMe();
  } catch (_) { /* keyingi urinishda */ }
  if (!state.me.consented || !(needsConnect() || state.login.step === 'done')) return;
  if (state.login.step === 'error') haptic('error');
  if (state.login.step === 'done') haptic('success');
  renderConnect();
}

// ---------- asosiy oyna ----------
const TABS = [
  ['home', 'Asosiy', 'home'], ['drafts', 'Qoralama', 'inbox'], ['tasks', 'Vazifalar', 'clock'], ['rules', 'Qoidalar', 'rules'],
  ['settings', 'Sozlamalar', 'settings'], ['account', 'Hisob', 'user'],
];

function go(tab) {
  if (state.tab === tab && !state.sub) return;
  state.tab = tab;
  state.sub = null;
  haptic('tap');
  window.scrollTo(0, 0);
  renderMain();
}

function renderNav() {
  nav.hidden = false;
  swap(nav, h('div', { class: 'inner' }, TABS.map(([k, label, ic]) => {
    const count = k === 'drafts' && state.me.pending_drafts > 0 ? h('span', { class: 'count' }, String(Math.min(state.me.pending_drafts, 99))) : null;
    return h('button', { class: state.tab === k ? 'on' : '', 'aria-label': label, onclick: () => go(k) }, icon(ic), label, count);
  })));
}

function renderMain() {
  renderNav();
  try { if (tg && tg.BackButton) { if (state.tab === 'home' && !state.sub) tg.BackButton.hide(); else tg.BackButton.show(); } } catch (_) { /* */ }
  const view = h('div', {});
  swap(root, view);
  if (state.sub) { state.sub(view); return; } // ichki sahifa (plaginlar, vazifa tahriri)
  const bar = accountBar();
  if (bar) put(view, bar);
  ({ home: viewHome, drafts: viewDrafts, tasks: viewTasks, rules: viewRules, settings: viewSettings, account: viewAccount, stats: viewStats, admin: viewAdmin })[state.tab](view);
}

const STATUS = {
  active: ['Faol', 'ok'], paused: ["To'xtatilgan", 'warn'], broken: ["Xavfsizlik uchun to'xtatilgan", 'bad'],
  pending: ['Ulanmagan', ''], revoked: ['Uzilgan', ''],
};
function statusBadge() {
  const [label, cls] = STATUS[state.me.status] || [state.me.status, ''];
  return h('span', { class: 'badge ' + cls }, label);
}

// ---------- Tariflar ----------
const TIER_NAMES = { advanced: 'Advanced', pro: 'Pro', trial: 'Sinov' };
const PLANS = [
  ['advanced', '⭐ Advanced', [
    'Cheksiz javob qoralamalari',
    'Uzun suhbat konteksti (20 xabar)',
    'Avto rejim, qoidalar va holatlar',
    "Kechki xulosa, va'dalar, taqvimga .ics",
    '⏰ Rejalashtirilgan AI vazifalar',
  ]],
  ['pro', '🚀 Pro', [
    "Advanced'dagi hammasi",
    '🎤 Ovozli xabarlarga javob',
    '📅 Google Calendar, Contacts, Tasks',
    '📱 Telefon avtomatikasi (Shortcuts / MacroDroid)',
    '🧩 Plaginlar: Telegram kanallar xulosasi, YouTube, Google Sheets, amoCRM/Bitrix24, yangiliklar, namoz vaqtlari',
    'Eng uzun kontekst (40 xabar)',
  ]],
];
function tiered(b) { return b && b.enabled && b.pro_price > 0; }
function tierLabel(b) { return tiered(b) && TIER_NAMES[b.tier] && b.tier !== 'trial' ? ` ${TIER_NAMES[b.tier]}` : ''; }
function needsPro() { const b = state.me.billing || {}; return b.enabled && !b.has_pro; }

// Pro funksiyasi yopiq bo'lsa — kartaning o'rniga taklif.
function proLock(title, text) {
  return h('div', { class: 'card' },
    h('div', { class: 'card-head' }, h('h3', {}, title), h('span', { class: 'badge accent' }, 'Pro')),
    h('p', { class: 'muted small' }, text),
    h('button', { class: 'btn ghost block', onclick: () => { go('account'); } }, `Pro tarifga o'tish · ${state.me.billing.pro_price} ⭐`));
}

function billingLine(b) {
  if (!b || !b.enabled || b.exempt) return null;
  if (b.paid) return b.canceled ? `Obuna${tierLabel(b)} ${fmtDate(b.paid_until)} gacha (yangilanmaydi)` : `Obuna${tierLabel(b)} faol · keyingi to'lov ${fmtDate(b.paid_until)}`;
  if (b.bonus) return `🎁 Bonus · ${b.days_left} kun qoldi`;
  if (b.on_trial) return `Bepul sinov · ${b.days_left} kun qoldi`;
  return 'Obuna faol emas';
}

// ---------- Asosiy ----------
function viewHome(view) {
  const me = state.me;
  const b = me.billing || {};
  const name = state.settings.owner_name || (tg && tg.initDataUnsafe && tg.initDataUnsafe.user && tg.initDataUnsafe.user.first_name) || '';
  const running = me.status === 'active';
  const modeName = { draft: 'Qoralama', auto: 'Avtomatik', off: "O'chiq" }[state.settings.mode] || state.settings.mode;

  if (me.admin && !isChild()) put(view, adminEntry());
  put(view, 
    h('div', { class: 'hero' },
      h('div', { class: 'eyebrow' }, 'Kotib'),
      h('h1', {}, name ? `Salom, ${name}` : 'Salom')),
    b.enabled && !b.active ? h('div', { class: 'card accent' },
      h('h3', {}, 'Obuna faol emas'),
      h('p', { class: 'small' }, 'Kotib yangi xabarlarga javob tayyorlamayapti.'),
      h('button', { class: 'btn', onclick: () => go('account') }, `Obunani yoqish · ${b.price} ⭐`),
      h('p', { class: 'muted small' }, "Yoki do'stingizni taklif qiling — u ulansa, sizga bepul kunlar qo'shiladi.")) : null,
    b.enabled && b.on_trial && b.days_left <= 3 ? h('div', { class: 'card soft' },
      h('h3', {}, `🎁 Sinov ${b.days_left} kunda tugaydi`),
      h('p', { class: 'small muted' }, "Do'stingizni taklif qiling: u Telegram akkauntini ulasa — sizga +7 kun, obuna bo'lsa — +30 kun bepul."),
      h('button', { class: 'btn ghost sm', onclick: () => go('account') }, "Do'stni taklif qilish")) : null,
    (() => {
      const q = state.settings.quiet_hours, info = q && quietInfo(q);
      return info && info.active ? h('div', { class: 'card accent' },
        h('h3', {}, 'Hozir tinch soatlar'),
        h('p', { class: 'small' }, `${q.start} – ${q.end}: kotib yangi xabarlarga javob tayyorlamaydi.`),
        h('button', { class: 'btn ghost sm', onclick: () => go('settings') }, "O'zgartirish")) : null;
    })(),
    me.status === 'broken' ? h('div', { class: 'card danger' },
      h('h3', {}, "Xavfsizlik uchun to'xtatildi"),
      h('p', { class: 'small muted' }, (me.note ? me.note + '. ' : '') + "Telegram cheklov qo'ygan bo'lishi mumkin. Bir necha soat kutib, keyin davom ettiring.")) : null,
    h('div', { class: 'card' },
      h('div', { class: 'card-head' }, h('h3', {}, 'Holat'), statusBadge()),
      h('p', { class: 'muted small' }, `Rejim: ${modeName}` + (billingLine(b) ? ' · ' + billingLine(b) : '')),
      h('div', { class: 'actions' }, running
        ? h('button', { class: 'btn ghost', onclick: busy(async () => { await api('POST', '/pause'); await refreshMe(); toast("To'xtatildi"); renderMain(); }) }, "To'xtatish")
        : h('button', { class: 'btn', onclick: busy(async () => { await api('POST', '/resume'); await refreshMe(); toast('Davom ettirildi'); renderMain(); }) }, 'Davom ettirish'))),
    awayCard(),
    h('div', { class: 'stats' },
      h('button', { class: 'stat', onclick: () => go('drafts') }, h('div', { class: 'num' }, String(me.pending_drafts || 0)), h('div', { class: 'lbl' }, 'Kutilayotgan qoralama')),
      h('button', { class: 'stat', onclick: () => go('rules') }, h('div', { class: 'num' }, String(state.settings.rules.length)), h('div', { class: 'lbl' }, 'Faol qoidalar'))),
    me.stats && me.stats.month_sent > 0 ? h('div', { class: 'card accent' },
      h('p', {}, h('b', {}, `⏱ Bu oy ~${me.stats.month_saved} tejaldi`)),
      h('p', { class: 'muted small' }, `Kotib ${me.stats.month_sent} ta javob yubordi — har biri taxminan 1 daqiqa vaqtingiz.`)) : null,
    h('button', { class: 'btn ghost block', onclick: () => go('stats') }, '📊 Batafsil statistika' + (needsPro() ? ' · Pro' : '')),
    h('h2', {}, 'Maslahatlar'),
    h('div', { class: 'card soft' },
      h('ul', { class: 'perks' },
        h('li', {}, "«Sozlamalar»da ismingizni yozing — javoblar tabiiyroq chiqadi"),
        h('li', {}, "«Qoidalar»da oila yoki ishdagilar uchun alohida uslub bering"),
        h('li', {}, "«Vazifalar»da AI'ga topshiriq rejalashtiring: masalan, har kuni 8:00 da ob-havo va valyuta kursi"),
        h('li', {}, 'Bot menyusidagi /pause — kotibni bir zumda to\'xtatadi'))),
  );
}

// ---------- Qoralamalar ----------
async function viewDrafts(view) {
  const head = h('div', { class: 'card-head' }, h('h1', {}, 'Qoralamalar'),
    h('button', { class: 'btn ghost sm', onclick: busy(async () => { await loadDrafts(); renderMain(); }) }, 'Yangilash'));
  put(view, head, h('div', { class: 'skeleton' }));
  try { await loadDrafts(); } catch (e) { swap(view, head, h('p', { class: 'err' }, e.message)); return; }
  if (state.tab !== 'drafts') return;
  if (!state.drafts.length) {
    swap(view, head, h('div', { class: 'empty' },
      h('div', { class: 'big' }, "Hozircha bo'sh"),
      h('p', { class: 'muted' }, 'Kontaktlaringiz yozganda javob qoralamasi shu yerda paydo bo\'ladi va bot sizga xabar beradi.')));
    return;
  }
  swap(view, head, h('p', { class: 'muted small' }, 'Matnni tahrirlab, keyin yuboring. 7 kundan eski qoralamalar avtomatik o\'chadi.'),
    ...state.drafts.map(draftCard));
}

async function loadDrafts() {
  state.drafts = await api('GET', '/drafts');
  state.me.pending_drafts = state.drafts.length;
  renderNav();
}

function draftCard(d) {
  const text = h('textarea', { maxlength: 4000, 'aria-label': 'Javob matni' }, d.reply);
  const counter = h('div', { class: 'counter' });
  const upd = () => { counter.textContent = text.value.length + ' / 4000'; counter.className = 'counter' + (text.value.length > 4000 ? ' over' : ''); };
  text.addEventListener('input', upd); upd();
  const removeCard = () => {
    card.remove();
    state.drafts = state.drafts.filter((x) => x.id !== d.id);
    state.me.pending_drafts = state.drafts.length;
    renderNav();
  };
  const card = h('div', { class: 'card' },
    h('div', { class: 'bubble' }, d.incoming),
    h('div', { class: 'meta' }, h('span', {}, fmtAgo(d.at)), d.reason ? h('span', { class: 'reason' }, d.reason) : null),
    h('label', { class: 'label' }, 'Javob'),
    text, counter,
    h('div', { class: 'actions' },
      h('button', { class: 'btn ghost', onclick: busy(async () => { await api('POST', `/drafts/${d.id}/reject`); removeCard(); toast('Rad etildi'); }) }, 'Rad etish'),
      h('button', { class: 'btn', onclick: busy(async () => {
        if (!text.value.trim()) throw new Error("Javob bo'sh");
        await bioGate('Javobni yuborish');
        await api('POST', `/drafts/${d.id}/send`, { reply: text.value });
        removeCard(); toast('Yuborildi');
      }) }, 'Yuborish')));
  return card;
}

// ---------- Rejalashtirilgan vazifalar ----------
const KINDS = [['once', 'Bir marta'], ['hourly', 'Har soat'], ['daily', 'Har kuni'], ['weekdays', 'Ish kunlari'], ['weekly', 'Tanlangan kunlar']];
const WEEK = [[1, 'Du'], [2, 'Se'], [3, 'Ch'], [4, 'Pa'], [5, 'Ju'], [6, 'Sh'], [0, 'Ya']];
const TASK_TEMPLATES = [
  ['🌤 Ertalabki ob-havo', 'daily', '07:30', "Bugungi ob-havo qanday? Qisqa ayt va nima kiyish haqida maslahat ber."],
  ['💱 Valyuta kursi', 'weekdays', '10:00', "Dollar, yevro va rubl kursini kechagiga nisbatan o'zgarishi bilan ayt."],
  ['📰 Kun yangiliklari', 'daily', '20:00', "O'zbekiston va dunyoning bugungi eng muhim 5 ta yangiligini qisqa xulosa qil, manbalari bilan."],
  ['▶️ YouTube yangi videolar', 'weekly', '19:00', "YouTube'da oxirgi 7 kunda chiqqan Go dasturlash bo'yicha eng yaxshi 5 ta videoni top."],
  ['💡 Motivatsiya', 'weekdays', '08:00', "Ish kunini boshlash uchun qisqa, samimiy motivatsiya yoz (2–3 jumla)."],
  ['📢 Kanallar xulosasi', 'daily', '21:00', "Telegram kanallarimdagi bugungi postlarni o'qib, eng muhim 10 ta yangilikni havolalari bilan qisqa xulosa qil."],
  ['🕌 Namoz vaqtlari', 'daily', '04:30', "Bugungi namoz vaqtlarini yubor."],
  ['📊 Kunlik savdo', 'daily', '21:30', "Google jadvalimdagi bugungi buyurtmalar soni va umumiy summasini hisobla."],
  ['💼 CRM vazifalari', 'weekdays', '09:00', "CRM'dagi bugungi va muddati o'tgan vazifalarimni ro'yxat qil."],
];

// fmtRun — vazifa vaqti egasining vaqt mintaqasida (Sozlamalar → Tinch soatlar), qurilmaniki emas.
function fmtRun(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  const o = { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' };
  try { return d.toLocaleString('uz-UZ', Object.assign({ timeZone: state.taskTZ || undefined }, o)); } catch (_) { return d.toLocaleString('uz-UZ', o); }
}

function todayISO() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

async function viewTasks(view) {
  const head = h('div', { class: 'card-head' }, h('h1', {}, 'Vazifalar'),
    h('button', { class: 'btn ghost sm', onclick: () => openPlugins() }, '🧩 Plaginlar'));
  put(view, head, h('p', { class: 'muted' }, "AI'ga topshiriq bering — kotib uni belgilangan vaqtda bajarib, natijani bot chatiga yuboradi. Bir martalik yoki takroriy."),
    h('div', { class: 'skeleton' }));
  let data;
  try { data = await api('GET', '/schedules'); } catch (e) { swap(view, head, h('p', { class: 'err' }, e.message)); return; }
  if (state.tab !== 'tasks' || state.sub) return;
  state.taskTZ = data.tz;
  const b = state.me.billing || {};
  const items = data.items || [];
  const kids = [head, h('p', { class: 'muted' }, "AI'ga topshiriq bering — kotib uni belgilangan vaqtda bajarib, natijani bot chatiga yuboradi. Bir martalik yoki takroriy.")];
  if (!data.allowed) {
    kids.push(h('div', { class: 'card' },
      h('div', { class: 'card-head' }, h('h3', {}, 'Rejalashtirilgan vazifalar'), h('span', { class: 'badge accent' }, 'Advanced')),
      h('p', { class: 'muted small' }, "Vazifalar Advanced va Pro tariflarida ishlaydi: AI belgilangan vaqtda topshiriqni bajarib, natijani bot chatiga yuboradi."),
      h('button', { class: 'btn ghost block', onclick: () => go('account') }, b.price ? `Advanced tarifga o'tish · ${b.price} ⭐` : 'Obunani yoqish')));
  }
  kids.push(data.plugins_allowed ? h('div', { class: 'card soft' },
    h('p', { class: 'small' }, h('b', {}, 'Ulangan plaginlar: '), data.plugins && data.plugins.length ? data.plugins.join(', ') : "yo'q"),
    h('p', { class: 'small muted' }, "Plaginlar AI'ga Telegram kanallaringiz, YouTube, Google Sheets, CRM, yangiliklar, namoz vaqtlari kabi imkoniyatlar beradi."),
    h('button', { class: 'btn ghost sm', onclick: () => openPlugins() }, 'Plaginlarni sozlash'))
    : h('div', { class: 'card soft' },
      h('div', { class: 'card-head' }, h('h3', {}, '🧩 Plaginlar'), h('span', { class: 'badge accent' }, 'Pro')),
      h('p', { class: 'small muted' }, "Pro tarifda vazifalar Telegram kanallaringizni o'qiydi, YouTube, Google Sheets, CRM, yangiliklar va namoz vaqtlaridan foydalanadi. Hozir vazifalar plaginsiz bajariladi."),
      h('button', { class: 'btn ghost sm', onclick: () => openPlugins() }, "Ko'rish")));
  if (!items.length) {
    kids.push(h('div', { class: 'empty' }, h('div', { class: 'big' }, "Hali vazifa yo'q"), h('p', { class: 'muted' }, 'Quyidagi tayyor namunalardan boshlang yoki o\'zingiz yozing.')));
  } else {
    kids.push(...items.map((t) => taskCard(t, data.allowed)));
  }
  if (!data.allowed) {
    if (items.length) kids.push(h('p', { class: 'muted small center' }, "Obuna faollashgach vazifalar yana bajariladi."));
  } else if (items.length < data.max) {
    kids.push(h('button', { class: 'btn block', onclick: () => openTaskEditor(null) }, '+ Yangi vazifa'));
    kids.push(h('h2', {}, 'Tayyor namunalar'),
      h('div', { class: 'chips' }, TASK_TEMPLATES.map(([title, kind, at, prompt]) => h('button', { class: 'chip-btn', onclick: () =>
        openTaskEditor({ title: title.replace(/^\S+\s/, ''), prompt, kind, at, weekdays: kind === 'weekly' ? 2 : 0, date: '', enabled: true }) }, title))));
  } else {
    kids.push(h('p', { class: 'muted small center' }, `Ko'pi bilan ${data.max} ta vazifa.`));
  }
  kids.push(h('p', { class: 'muted small center' }, `Vaqt mintaqasi: ${data.tz} (Sozlamalar → Tinch soatlar).`));
  swap(view, kids);
}

function taskStatus(t) {
  if (t.last_status === 'running') return h('span', { class: 'badge accent' }, 'Bajarilmoqda…');
  if (!t.enabled) return h('span', { class: 'badge' }, t.runs && t.kind === 'once' ? 'Bajarildi' : "O'chiq");
  if (t.last_status === 'error') return h('span', { class: 'badge bad' }, 'Xato');
  return h('span', { class: 'badge ok' }, 'Faol');
}

function taskCard(t, allowed) {
  const result = t.last_result ? h('details', {}, h('summary', { class: 'small' }, `Oxirgi natija · ${fmtRun(t.last_run)}`),
    h('p', { class: 'small pre' }, t.last_result)) : null;
  return h('div', { class: 'card' },
    h('div', { class: 'card-head' }, h('h3', {}, t.title), taskStatus(t)),
    h('p', { class: 'small muted' }, '🕒 ' + t.describe + (t.enabled && t.next_run ? ` · keyingisi ${fmtRun(t.next_run)}` : '') + (t.runs ? ` · ${t.runs} marta bajarildi` : '')),
    t.target_id ? h('p', { class: 'small' }, `📨 ${t.target_name || 'Kontakt'}ga · ${t.target_auto ? 'tasdiqsiz yuboriladi' : 'avval sizga qoralama'}`) : null,
    result,
    !allowed ? h('div', { class: 'row' }, h('button', { class: 'btn danger-ghost sm', onclick: busy(async () => {
      if (!(await confirmBox(`«${t.title}» vazifasi o'chirilsinmi?`))) return;
      await api('DELETE', `/schedules/${t.id}`); toast("O'chirildi"); renderMain();
    }) }, "O'chirish")) :
    h('div', { class: 'row' },
      h('button', { class: 'btn ghost sm', onclick: busy(async () => {
        await api('POST', `/schedules/${t.id}/run`);
        toast(t.target_id ? (t.target_auto ? `Bajarilmoqda — natija ${t.target_name || 'kontakt'}ga yuboriladi` : 'Bajarilmoqda — qoralama sizga keladi') : 'Bajarilmoqda — natija bot chatiga keladi');
        setTimeout(() => { if (state.tab === 'tasks' && !state.sub) renderMain(); }, 1500);
      }) }, '▶ Hozir bajarish'),
      h('button', { class: 'btn ghost sm', onclick: () => openTaskEditor(t) }, 'Tahrirlash'),
      switchMini(t.enabled, async (on) => {
        await api('PUT', `/schedules/${t.id}`, taskPayload(t, { enabled: on }));
        toast(on ? 'Yoqildi' : "O'chirildi"); renderMain();
      })));
}

// switchMini — kartadagi kichik yoqish/o'chirish tugmasi (xato bo'lsa holat qaytadi).
function switchMini(checked, onchange) {
  const input = h('input', { type: 'checkbox', checked: !!checked, 'aria-label': 'Yoqish', onchange: async (e) => {
    haptic('tap');
    const v = e.target.checked;
    try { await onchange(v); } catch (err) { e.target.checked = !v; fail(err); }
  } });
  return h('label', { class: 'switch' }, input, h('span', {}));
}

function taskPayload(t, over) {
  const p = { title: t.title || '', prompt: t.prompt || '', kind: t.kind, at: t.at, date: t.date || '', weekdays: t.weekdays || 0, enabled: !!t.enabled,
    target_id: t.target_id || 0, target_auto: !!t.target_auto };
  return Object.assign(p, over || {});
}

function openTaskEditor(t) {
  state.sub = (view) => taskEditor(view, t);
  haptic('tap'); window.scrollTo(0, 0); renderMain();
}
function closeSub() { state.sub = null; renderMain(); }

function taskEditor(view, orig) {
  const editing = !!(orig && orig.id);
  const t = Object.assign({ title: '', prompt: '', kind: 'daily', at: '09:00', date: todayISO(), weekdays: 2, enabled: true }, orig || {});
  if (!t.date) t.date = todayISO();
  if (!t.weekdays) t.weekdays = 2;
  const title = h('input', { type: 'text', maxlength: 80, value: t.title, placeholder: "Masalan: Ertalabki ob-havo" });
  const prompt = h('textarea', { maxlength: 2000, rows: 5, placeholder: "AI nima qilsin? Masalan: «Bugungi Toshkent ob-havosini va dollar kursini ayt»" }, t.prompt);
  const counter = h('div', { class: 'counter' });
  const upd = () => { counter.textContent = prompt.value.length + ' / 2000'; };
  prompt.addEventListener('input', upd); upd();
  const when = h('div', {});
  const kindSeg = h('div', { class: 'chips' });
  const drawKind = () => {
    swap(kindSeg, KINDS.map(([k, label]) => h('button', { class: 'chip-btn' + (t.kind === k ? ' on' : ''), onclick: () => { t.kind = k; haptic('tap'); drawKind(); } }, label)));
    const time = h('input', { type: 'time', value: t.at, onchange: (e) => { t.at = e.target.value; } });
    const parts = [];
    if (t.kind === 'once') parts.push(field('Sana', h('input', { type: 'date', value: t.date, min: todayISO(), onchange: (e) => { t.date = e.target.value; } })));
    if (t.kind === 'weekly') {
      parts.push(field('Kunlar', h('div', { class: 'chips' }, WEEK.map(([d, label]) => h('button', { class: 'chip-btn' + (t.weekdays & (1 << d) ? ' on' : ''),
        onclick: (e) => { t.weekdays ^= (1 << d); e.currentTarget.classList.toggle('on'); haptic('tap'); } }, label)))));
    }
    if (t.kind === 'hourly') {
      const min = h('select', { onchange: (e) => { t.at = '00:' + e.target.value; } },
        ['00', '05', '10', '15', '20', '25', '30', '35', '40', '45', '50', '55'].map((m) => h('option', { value: m, selected: t.at.slice(3) === m }, ':' + m)));
      if (!/^00:(00|05|10|15|20|25|30|35|40|45|50|55)$/.test(t.at)) t.at = '00:00';
      parts.push(field('Har soatning qaysi daqiqasida', min));
    } else {
      parts.push(field('Vaqt', time, "Sizning vaqt mintaqangizda (Sozlamalar → Tinch soatlar)."));
    }
    swap(when, parts);
  };
  drawKind();
  const target = targetPicker(t);
  const save = busy(async () => {
    if (t.target_mode === 'contact' && !t.target_id) throw new Error('Kontaktni tanlang');
    if (t.target_mode !== 'contact') { t.target_id = 0; t.target_auto = false; }
    const body = taskPayload(t, { title: title.value, prompt: prompt.value, enabled: editing ? !!t.enabled : true });
    if (t.kind === 'weekly' && !t.weekdays) throw new Error('Kamida bitta kunni tanlang');
    if (editing) await api('PUT', `/schedules/${t.id}`, body); else await api('POST', '/schedules', body);
    toast(editing ? 'Saqlandi' : 'Vazifa yaratildi');
    closeSub();
  });
  put(view,
    h('div', { class: 'card-head' }, h('h1', {}, editing ? 'Vazifani tahrirlash' : 'Yangi vazifa'),
      h('button', { class: 'btn ghost sm', onclick: closeSub }, '← Orqaga')),
    h('div', { class: 'card' },
      field("AI uchun ko'rsatma", [prompt, counter], "Aniq yozing: nima kerak, qanday ko'rinishda. Kerak bo'lsa AI ulangan plaginlardan (internet, YouTube, ob-havo...) foydalanadi."),
      field('Nomi (ixtiyoriy)', title)),
    h('div', { class: 'card' }, field('Qachon', kindSeg), when),
    target,
    h('button', { class: 'btn block', onclick: save }, editing ? 'Saqlash' : 'Yaratish'),
    editing ? h('button', { class: 'btn danger-ghost block', onclick: busy(async () => {
      if (!(await confirmBox(`«${t.title}» vazifasi o'chirilsinmi?`))) return;
      await api('DELETE', `/schedules/${t.id}`); toast("O'chirildi"); closeSub();
    }) }, "Vazifani o'chirish") : null);
  setTimeout(() => { if (!editing && !t.prompt) prompt.focus(); }, 50);
}

// targetPicker — natija kimga: faqat menga (bot chati) yoki kontaktga (Pro), tasdiqli yoki tasdiqsiz.
function targetPicker(t) {
  t.target_mode = t.target_id ? 'contact' : 'me';
  const card = h('div', { class: 'card' });
  const draw = () => {
    const seg = h('div', { class: 'segmented' }, [['me', 'Faqat menga'], ['contact', 'Kontaktga']].map(([k, label]) =>
      h('button', { class: t.target_mode === k ? 'on' : '', onclick: () => { t.target_mode = k; haptic('tap'); draw(); } }, label)));
    const parts = [field('Natijani kimga yuborish', seg, t.target_mode === 'me' ? 'Natija bot chatiga, faqat sizga keladi.' : null)];
    if (t.target_mode === 'contact') {
      if (needsPro()) {
        parts.push(h('p', { class: 'small' }, "Natijani kontaktga yuborish Pro tarifda."),
          h('button', { class: 'btn ghost block', onclick: () => go('account') }, `Pro tarifga o'tish · ${state.me.billing.pro_price} ⭐`));
      } else if (!state.contacts) {
        parts.push(h('div', { class: 'skeleton' }));
        api('GET', '/contacts').then((cs) => { state.contacts = cs || []; }).catch(() => { state.contacts = []; }).finally(draw);
      } else {
        const opts = [['', 'Kontaktni tanlang…'], ...state.contacts.map((c) => [String(c.id), c.name])];
        if (t.target_id && !state.contacts.some((c) => c.id === t.target_id)) opts.push([String(t.target_id), t.target_name || ('#' + t.target_id)]);
        const sel = h('select', { onchange: (e) => {
          t.target_id = +e.target.value || 0;
          const c = state.contacts.find((x) => x.id === t.target_id);
          t.target_name = c ? c.name : '';
        } }, opts.map(([v, l]) => h('option', { value: v, selected: String(t.target_id || '') === v }, l)));
        parts.push(field('Kontakt', sel, state.contacts.length ? "Faqat Telegram kontaktlaringiz. Xabar sizning akkauntingizdan, siz nomingizdan ketadi." : "Kontaktlar yuklanmadi — Telegram akkaunt ulanganini tekshiring."),
          switchRow('Tasdiqsiz yuborish', "O'chiq bo'lsa, natija avval sizga qoralama bo'lib keladi va bir bosishda yuborasiz. Havola, pul yoki parol mavzusidagi matn har doim qoralama bo'ladi; kuniga 20 tadan ortig'i ham.",
            t.target_auto, (v) => { t.target_auto = v; }, true),
          h('p', { class: 'muted small' }, "Ko'rsatmani shu odamga yoziladigan xabar sifatida yozing, masalan: «Onamga xayrli tong tilagini yoz, iliq va qisqa»."));
      }
    }
    swap(card, parts);
  };
  draw();
  return card;
}

// ---------- Plaginlar ----------
function openPlugins() {
  state.sub = viewPlugins;
  haptic('tap'); window.scrollTo(0, 0); renderMain();
}

async function viewPlugins(view) {
  const head = h('div', { class: 'card-head' }, h('h1', {}, 'Plaginlar'), h('button', { class: 'btn ghost sm', onclick: closeSub }, '← Orqaga'));
  put(view, head, h('div', { class: 'skeleton' }));
  let data;
  try { data = await api('GET', '/plugins'); } catch (e) { swap(view, head, h('p', { class: 'err' }, e.message)); return; }
  if (state.sub !== viewPlugins) return;
  const list = data.items || [];
  const ready = list.filter((p) => !p.soon), soon = list.filter((p) => p.soon);
  const b = state.me.billing || {};
  swap(view, head,
    h('p', { class: 'muted' }, "Plaginlar (connector'lar) kotibga tashqi xizmatlardan foydalanish imkonini beradi. Yoqilgan plaginlardan rejalashtirilgan vazifalar foydalanadi."),
    data.allowed ? null : h('div', { class: 'card accent' },
      h('div', { class: 'card-head' }, h('h3', {}, 'Plaginlar Pro tarifda'), h('span', { class: 'badge accent' }, 'Pro')),
      h('p', { class: 'small' }, "Telegram kanallar xulosasi, YouTube, Google Sheets, CRM va boshqa plaginlarni ulash uchun Pro tarifga o'ting."),
      h('button', { class: 'btn block', onclick: () => go('account') }, b.pro_price ? `Pro tarifga o'tish · ${b.pro_price} ⭐` : 'Obunani yoqish')),
    ready.map((p) => pluginCard(p, data.allowed)),
    soon.length ? h('h2', {}, 'Tez orada') : null,
    soon.length ? h('div', { class: 'card soft' }, h('ul', { class: 'plugin-soon' }, soon.map((p) =>
      h('li', {}, h('b', {}, `${p.icon} ${p.name}`), h('div', { class: 'small muted' }, p.description))))) : null);
}

function pluginCard(p, allowed) {
  const card = h('div', { class: 'card' });
  if (!allowed) {
    // Pro'siz: faqat tavsif; avval ulangan bo'lsa — uzish mumkin.
    put(card, h('div', { class: 'card-head' }, h('h3', {}, `${p.icon} ${p.name}`), h('span', { class: 'badge' }, '🔒 Pro')),
      h('p', { class: 'small muted' }, p.description),
      p.configured ? h('button', { class: 'btn danger-ghost sm', onclick: busy(async () => {
        await api('DELETE', `/plugins/${p.id}`); toast('Uzildi'); renderMain();
      }) }, 'Uzish') : null);
    return card;
  }
  if (p.connect) return accountPluginCard(p, card);
  const draw = (open) => {
    const inputs = {};
    const fields = (p.fields || []).map((f) => {
      let control;
      if (f.type === 'select') {
        control = h('select', {}, f.options.map((o) => h('option', { value: o.value, selected: (p.config[f.key] || f.default) === o.value }, o.label)));
      } else {
        control = h('input', { type: f.type === 'secret' ? 'password' : 'text', maxlength: 200, autocomplete: 'off',
          value: f.type === 'secret' ? '' : (p.config[f.key] || ''),
          placeholder: f.type === 'secret' && p.config[f.key] ? `Saqlangan: ${p.config[f.key]} (o'zgartirish uchun yangisini yozing)` : (f.placeholder || f.default || '') });
      }
      inputs[f.key] = control;
      return field(f.label + (f.required ? '' : ' (ixtiyoriy)'), control, f.help);
    });
    const collect = () => Object.fromEntries(Object.entries(inputs).map(([k, el]) => [k, el.value]));
    const saveWith = async (enabled) => {
      const res = await api('PUT', `/plugins/${p.id}`, { enabled, config: collect() });
      Object.assign(p, res); toast(enabled ? `${p.name} yoqildi` : `${p.name} o'chirildi`); draw(false);
    };
    swap(card,
      h('div', { class: 'card-head' }, h('h3', {}, `${p.icon} ${p.name}`),
        p.fields && p.fields.length && !p.configured && !open ? h('span', { class: 'badge' }, p.category) : switchMini(p.enabled, async (on) => {
          if (on && p.fields && p.fields.some((f) => f.required) && !p.configured) { draw(true); throw new Error('Avval sozlang'); }
          await saveWith(on);
        })),
      h('p', { class: 'small muted' }, p.description),
      p.examples && p.examples.length ? h('p', { class: 'small' }, '💡 ' + p.examples[0]) : null,
      open ? h('div', { class: 'stack' }, fields,
        h('button', { class: 'btn block', onclick: busy(() => saveWith(true)) }, 'Saqlash va yoqish'),
        p.configured ? h('button', { class: 'btn danger-ghost block', onclick: busy(async () => {
          if (!(await confirmBox(`${p.name} uzilsinmi? Sozlamalari (API kalit) o'chiriladi.`))) return;
          await api('DELETE', `/plugins/${p.id}`); Object.assign(p, { enabled: false, configured: false, config: {} }); toast('Uzildi'); draw(false);
        }) }, 'Uzish') : null,
        h('button', { class: 'btn ghost block', onclick: () => draw(false) }, 'Bekor qilish'))
        : (p.fields && p.fields.length ? h('button', { class: 'btn ghost sm', onclick: () => draw(true) }, p.configured ? 'Sozlash' : 'Ulash') : null));
  };
  draw(false);
  return card;
}

// accountPluginCard — akkaunt orqali ulanadigan plagin (YouTube): API kalit yo'q, faqat
// «Ulash» tugmasi — Google rozilik sahifasi tashqi brauzerda ochiladi.
function accountPluginCard(p, card) {
  const draw = () => {
    const connected = p.configured;
    // Ulangandan keyingi sozlamalar (masalan, Google Sheets — jadval havolasi).
    const inputs = {};
    const fields = connected ? (p.fields || []).map((f) => {
      const control = f.type === 'select'
        ? h('select', {}, f.options.map((o) => h('option', { value: o.value, selected: (p.config[f.key] || f.default) === o.value }, o.label)))
        : h('input', { type: 'text', maxlength: 200, autocomplete: 'off', value: p.config[f.key] || '', placeholder: f.placeholder || '' });
      inputs[f.key] = control;
      return field(f.label, control, f.help);
    }) : [];
    const collect = () => Object.fromEntries(Object.entries(inputs).map(([k, el]) => [k, el.value]));
    const missing = (p.fields || []).filter((f) => f.required && !p.config[f.key]);
    const save = async (on) => {
      Object.assign(p, await api('PUT', `/plugins/${p.id}`, { enabled: on, config: collect() }));
      toast(on ? `${p.name} yoqildi` : `${p.name} o'chirildi`);
    };
    swap(card,
      h('div', { class: 'card-head' }, h('h3', {}, `${p.icon} ${p.name}`),
        connected ? switchMini(p.enabled, save) : h('span', { class: 'badge' }, 'Ulanmagan')),
      h('p', { class: 'small muted' }, p.description),
      connected ? h('p', { class: 'small' }, '✅ Ulangan' + (p.account ? `: ${p.account}` : '')) : null,
      connected && missing.length ? h('p', { class: 'small err' }, 'Yana kerak: ' + missing.map((f) => f.label).join(', ')) : null,
      p.examples && p.examples.length ? h('p', { class: 'small' }, '💡 ' + p.examples[0]) : null,
      connected
        ? h('div', { class: 'stack' }, fields,
          fields.length ? h('button', { class: 'btn block', onclick: busy(async () => { await save(true); draw(); }) }, 'Saqlash') : null,
          h('button', { class: 'btn danger-ghost sm', onclick: busy(async () => {
            if (!(await confirmBox(`${p.name} uzilsinmi? Ruxsat Google'da ham bekor qilinadi.`))) return;
            await api('DELETE', `/plugins/${p.id}`); Object.assign(p, { enabled: false, configured: false, account: '', config: {} }); toast('Uzildi'); draw();
          }) }, 'Uzish'))
        : h('div', { class: 'stack' },
          h('button', { class: 'btn block', onclick: busy(async () => {
            const { url } = await api('POST', `/plugins/${p.id}/connect`);
            // Google ichki brauzerda (WebView) kirishni bloklaydi — tashqi brauzerda ochamiz.
            if (tg && tg.openLink) tg.openLink(url); else window.open(url, '_blank', 'noopener');
            toast("Brauzerda akkauntni tanlab, ruxsat bering, so'ng shu yerga qayting");
          }) }, `${p.icon} ${p.name}ni ulash`),
          h('p', { class: 'small muted' }, "Kalit yoki parol kerak emas: Google sahifasida akkauntingizni tanlab «Ruxsat berish»ni bosasiz. Faqat o'qish ruxsati so'raladi."),
          h('button', { class: 'btn ghost sm', onclick: () => renderMain() }, 'Ulandimi? Yangilash')));
  };
  draw();
  return card;
}

// ---------- Sozlamalar ----------
function field(label, control, hint) {
  return h('div', { class: 'field' }, h('label', {}, label), control, hint ? h('div', { class: 'hint' }, hint) : null);
}
// local=true — sozlamalarga tegishli emas (saqlash tugmasi faollashmaydi).
function switchRow(title, desc, checked, onchange, local) {
  const input = h('input', { type: 'checkbox', checked: !!checked, onchange: (e) => { haptic('tap'); onchange(e.target.checked); if (!local) markDirty(); } });
  return h('label', { class: 'switch-row' },
    h('div', {}, h('div', { class: 't' }, title), desc ? h('div', { class: 'd' }, desc) : null),
    h('span', { class: 'switch' }, input, h('span', {})));
}
function select(options, value, onchange) {
  return h('select', { onchange: (e) => { onchange(e.target.value); markDirty(); } },
    options.map(([k, v]) => h('option', { value: k, selected: k === value }, v)));
}
function personaSelect(value, onchange) { return select(Object.entries(PERSONA_LABELS), value, onchange); }
function markDirty() { state.dirty = true; const b = document.getElementById('save'); if (b) b.disabled = false; }

function saveButton() {
  return h('button', { id: 'save', class: 'btn block', disabled: !state.dirty, onclick: busy(async () => {
    if (state.settings.mode === 'auto' && state.savedMode !== 'auto') await bioGate('Avtomatik rejimni yoqish');
    await api('PUT', '/settings', state.settings);
    state.savedMode = state.settings.mode;
    state.dirty = false; toast('Saqlandi');
    const b = document.getElementById('save'); if (b) b.disabled = true;
  }) }, 'Saqlash');
}

function modeControl(s) {
  const seg = h('div', { class: 'segmented', role: 'tablist' });
  const draw = () => swap(seg, ...[['draft', 'Qoralama'], ['auto', 'Avto'], ['off', "O'chiq"]].map(([k, v]) =>
    h('button', { class: s.mode === k ? 'on' : '', role: 'tab', onclick: async () => {
      if (k === 'auto' && s.mode !== 'auto') {
        const ok = await confirmBox("Avto-rejimda javoblar siz tasdiqlamasdan yuboriladi (sezgir mavzular bundan mustasno). Telegram cheklovi xavfi oshadi. Yoqilsinmi?");
        if (!ok) return;
      }
      s.mode = k; haptic('tap'); markDirty(); draw();
    } }, v)));
  draw();
  return seg;
}

function viewSettings(view) {
  const s = state.settings;
  const custom = h('textarea', { maxlength: 600, placeholder: "Masalan: qisqa va samimiy yoz, «siz» deb murojaat qil…" }, s.default_custom || '');
  custom.addEventListener('input', () => { s.default_custom = custom.value; markDirty(); });
  // Jonli izoh: 24 soatlik formatda (AM/PM chalkashligi bo'lmasin) va uzun oraliqqa ogohlantirish.
  const hint = h('div', {});
  const quietHint = () => {
    const q = s.quiet_hours;
    const info = quietInfo(q);
    if (!info) { swap(hint, q.enabled ? h('p', { class: 'hint' }, "Boshlanishi va tugashi bir xil bo'lmasin.") : null); return; }
    swap(hint,
      h('p', { class: 'hint' }, `Kotib har kuni ${q.start} dan ${q.end} gacha jim turadi (${info.hours} soat).` + (info.active ? ' Hozir — tinch soat.' : '')),
      info.hours > 12 ? h('div', { class: 'card accent' }, h('p', { class: 'small' },
        `Diqqat: bu kunning ${info.hours} soati. «11:00 AM» — kunduzi soat 11; tunni nazarda tutgan bo'lsangiz, PM ni tanlang (masalan 11:00 PM = 23:00).`)) : null);
  };
  quietHint();
  const tzList = TIMEZONES.some(([k]) => k === s.quiet_hours.tz) ? TIMEZONES : [[s.quiet_hours.tz, s.quiet_hours.tz], ...TIMEZONES];

  put(view, 
    h('h1', {}, 'Sozlamalar'),
    h('div', { class: 'card' },
      field('Ish rejimi', modeControl(s), 'Qoralama — siz tasdiqlaysiz (tavsiya). Avto — darhol yuboriladi.'),
      field('Ismingiz', h('input', { type: 'text', value: s.owner_name || '', maxlength: 64, placeholder: 'Masalan: Aziz',
        oninput: (e) => { s.owner_name = e.target.value; markDirty(); } }), 'AI javoblarni sizning nomingizdan yozadi.'),
      (() => {
        const input = h('input', { type: 'text', value: currentAway(s), maxlength: 100, placeholder: "Masalan: Hozir rulda / Dars qilyapman",
          oninput: (e) => { s.away = e.target.value.replace(/\s+/g, ' '); s.away_until = null; markDirty(); } });
        const pick = (text) => { input.value = text; s.away = text; s.away_until = null; markDirty(); haptic('tap'); };
        return field('Hozirgi holatingiz', [input,
          h('div', { class: 'chips' }, STATUS_PRESETS.map(([icon, label, away]) => h('button', { class: 'chip-btn', onclick: () => pick(away) }, `${icon} ${label}`)),
            h('button', { class: 'chip-btn', onclick: () => pick('') }, '✖️ Yo\'q'))],
        "Kotib javobda hisobga oladi. Bu yerda — muddatsiz; vaqtli holat uchun bosh sahifadagi «Hozir nima qilyapsiz?». Telefon avtomatikasi ham o'zgartira oladi.");
      })(),
      field('Javob tili', select([['auto', 'Suhbatdosh tilida'], ['uz', "O'zbekcha"], ['ru', 'Ruscha'], ['en', 'Inglizcha']], s.language, (v) => { s.language = v; })),
      field('Uslub', personaSelect(s.default_persona, (v) => { s.default_persona = v; })),
      field("O'z ko'rsatmangiz", custom, "Uslub «O'z ko'rsatmam» bo'lsa ishlatiladi.")),
    h('h2', {}, 'Kimga javob beriladi'),
    h('div', { class: 'card' },
      switchRow("Kontaktda yo'qlarga ham", 'Tavsiya etilmaydi: spam va firibgarlar ko\'p', s.reply_unknown, (v) => { s.reply_unknown = v; }),
      switchRow('Ovozli xabarlarga ham' + (needsPro() ? ' · Pro' : ''), (needsPro() ? 'Pro tarifda ishlaydi. ' : '') + "Kotib ovozli xabarni tinglab, javob qoralamasini tayyorlaydi (2 daqiqagacha). Ovozga javob doim faqat qoralama — avtomatik yuborilmaydi.", s.reply_voice, (v) => { s.reply_voice = v; }),
      switchRow('Rasmlarga ham' + (needsPro() ? ' · Pro' : ''), (needsPro() ? 'Pro tarifda ishlaydi. ' : '') + "Kotib rasmni ko'rib, javob qoralamasini tayyorlaydi. Rasmga javob doim faqat qoralama.", s.reply_photos, (v) => { s.reply_photos = v; }),
      switchRow('Botlarga ham', "Faqat qoralama: bot bilan cheksiz yozishma bo'lmasligi uchun avtomatik yuborilmaydi", s.reply_bots, (v) => { s.reply_bots = v; }),
      switchRow('Javobda havolalar', "O'chiq bo'lsa, havolali javoblar qoralamada qoladi", s.allow_links, (v) => { s.allow_links = v; }),
      switchRow('Uchrashuvlarni taqvimga taklif qilish', "Suhbatda aniq vaqtli uchrashuv chiqsa, bot «Taqvimga qo'shaymi?» deb so'raydi. Google ulanmagan bo'lsa ham ishlaydi (iPhone/Android taqvimi).", !s.no_suggest, (v) => { s.no_suggest = !v; }),
      switchRow('Kechki xulosa', "Har kuni soat 21:00 da: nechta javob tayyorlandi, kim javob kutyapti, qancha vaqt tejaldi.", !s.no_digest, (v) => { s.no_digest = !v; }),
      switchRow('Haftalik hisobot' + (needsPro() ? ' · Pro' : ''), "Har dushanba 9:00 da: o'tgan hafta statistikasi, o'zgarish, eng faol vaqt va kontaktlar.", !s.no_weekly, (v) => { s.no_weekly = !v; }),
      switchRow("«Keyinroq yozaman» eslatmasi", "Kotib kimgadir «keyinroq yozaman» desa va 3 soat ichida o'zingiz yozmasangiz — bot eslatadi.", !s.no_followups, (v) => { s.no_followups = !v; }),
      switchRow('Qoralamalar bot chatida', "Qoralama botga tugmalar bilan keladi: bir bosishda yuborasiz yoki xabarga javob qilib matnni o'zgartirasiz. Matnlar bot chatida saqlanib qoladi.", s.drafts_in_bot, (v) => { s.drafts_in_bot = v; })),
    h('h2', {}, 'Kunlik yordamchi'),
    notifyCard(s),
    h('h2', {}, 'Plaginlar'),
    h('div', { class: 'card' },
      h('p', { class: 'small muted' }, "Telegram kanallar xulosasi, namoz vaqtlari, yangiliklar, YouTube, Google Sheets, amoCRM/Bitrix24 — kotibga ulang. Rejalashtirilgan vazifalar ulardan foydalanadi."),
      h('div', { class: 'row' },
        h('button', { class: 'btn ghost sm', onclick: () => openPlugins() }, pro('🧩 Plaginlarni sozlash')),
        h('button', { class: 'btn ghost sm', onclick: () => go('tasks') }, '🕒 Vazifalar'))),
    h('h2', {}, "O'qitish rejimi"),
    styleCard(s),
    h('h2', {}, "O'zim haqimda"),
    aboutCard(s),
    h('h2', {}, 'Kun tartibim'),
    routineCard(s),
    h('h2', {}, 'Bilim bazasi'),
    knowledgeCard(s),
    h('h2', {}, 'Tinch soatlar'),
    h('div', { class: 'card' },
      switchRow('Tunda javob yozilmasin', 'Shu vaqt oralig\'ida kotib jim turadi', s.quiet_hours.enabled, (v) => { s.quiet_hours.enabled = v; quietHint(); }),
      h('div', { class: 'two' },
        field('Boshlanishi', h('input', { type: 'time', value: s.quiet_hours.start, onchange: (e) => { s.quiet_hours.start = e.target.value; markDirty(); quietHint(); } })),
        field('Tugashi', h('input', { type: 'time', value: s.quiet_hours.end, onchange: (e) => { s.quiet_hours.end = e.target.value; markDirty(); quietHint(); } }))),
      field('Vaqt mintaqasi', select(tzList, s.quiet_hours.tz, (v) => { s.quiet_hours.tz = v; quietHint(); })),
      hint),
    saveButton(),
  );
}

// ---------- Kunlik yordamchi: xulosa, brifing, eslatmalar ----------
const pro = (t) => t + (needsPro() ? ' · Pro' : '');
function notifyCard(s) {
  const hours = select([['1', '1 soat'], ['2', '2 soat'], ['4', '4 soat'], ['8', '8 soat'], ['24', '1 kun']],
    String(s.unanswered_hours || 4), (v) => { s.unanswered_hours = +v; });
  return h('div', { class: 'card' },
    switchRow(pro('AI xulosa: «Bugun nima bo\'ldi»'), "Kechki xulosa va ertalabki brifingga yozishmalar mazmuni qo'shiladi: kim nima so'radi, nima kelishildi, nima qilish kerak. Xabarlar xulosa uchun AI'ga yuboriladi, natija faqat bot chatiga keladi va saqlanmaydi.", s.smart_digest, (v) => { s.smart_digest = v; }),
    switchRow('Ertalabki brifing', "Har kuni 8:00 da: tunda kim yozdi, kutayotgan qoralamalar va bugungi taqvim.", s.morning_brief, (v) => { s.morning_brief = v; }),
    switchRow('Shoshilinch xabarlar', "«Shoshilinch», «срочно», «kasalxona» kabi xabarlar haqida darhol bildiradi — tinch soatlarda ham.", !s.no_urgent, (v) => { s.no_urgent = !v; }),
    switchRow('Javobsiz qolganlar', "Kontakt yozganiga ko'p vaqt bo'lib, hech kim javob bermagan bo'lsa — eslatadi (tinch soatlarda emas).", !s.no_unanswered, (v) => { s.no_unanswered = !v; }),
    field('Qancha vaqtdan keyin eslatilsin', hours),
    switchRow(pro('Ovozli xabar matni'), "Kontakt yuborgan ovozli xabar matnga o'girilib bot chatiga keladi — yig'ilishda ham o'qiysiz.", s.transcribe_voice, (v) => { s.transcribe_voice = v; }),
    switchRow(pro("Tug'ilgan kun tabriklari"), "Google Contacts'da tug'ilgan kuni bor kontaktlar uchun soat 9:00 da tabrik qoralamasi tayyorlanadi — siz tasdiqlaysiz. Google ulangan bo'lishi kerak.", !s.no_birthdays, (v) => { s.no_birthdays = !v; }),
    h('p', { class: 'hint' }, "Botga shunchaki yozing: «Aziz bilan nima kelishdik?» (/sor shart emas) — kotib yozishmani o'qib, javob beradi (Pro)."));
}

// ---------- O'qitish rejimi: «O'zimdek» uslubi ----------
function styleCard(s) {
  if (needsPro()) return proLock("O'qitish rejimi", "Kotib sizning haqiqiy yozishmalaringizdan uslubingizni o'rganadi va xuddi o'zingizdek javob yozadi. Soya rejimida bir hafta jim kuzatib, qanchalik mos kelishini o'lchaydi.");
  const card = h('div', { class: 'card' });
  const profile = h('textarea', { maxlength: 2000, placeholder: "Hali o'rganilmagan. «Uslubimni o'rganish» tugmasini bosing yoki o'zingiz yozing." }, s.style_profile || '');
  profile.addEventListener('input', () => { s.style_profile = profile.value; markDirty(); });
  const shadowOn = () => s.shadow_until && new Date(s.shadow_until) > new Date();
  const draw = () => {
    const meta = [];
    if (s.style_updated_at) meta.push('Yangilangan: ' + fmtAgo(s.style_updated_at));
    if (s.style_score > 0) meta.push(`moslik: ${s.style_score}%`);
    swap(card,
      h('div', { class: 'card-head' }, h('h3', {}, "🪞 O'zimdek"),
        shadowOn() ? h('span', { class: 'badge accent' }, 'Soya rejimi') : (s.style_profile ? h('span', { class: 'badge ok' }, "O'rganilgan") : null)),
      h('p', { class: 'muted small' }, "Kotib so'nggi yozishmalaringizdan uslubingizni (uzunlik, murojaat, iboralar, emoji) o'rganadi. Uslub sifatida «O'zimdek»ni tanlang."),
      meta.length ? h('p', { class: 'hint' }, meta.join(' · ')) : null,
      h('button', { class: 'btn ghost block', onclick: busy(async () => {
        await api('POST', '/style/learn');
        toast("O'rganilmoqda… Tayyor bo'lganda bot xabar beradi");
      }) }, "Uslubimni o'rganish"),
      switchRow("Tahrirlarimdan o'rganish", "Qoralamani o'zgartirib yuborsangiz, kotib farqdan o'rganadi; profil har kuni tunda yangilanadi.", s.learn, (v) => { s.learn = v; }),
      shadowOn()
        ? h('div', { class: 'card accent' },
          h('p', { class: 'small' }, `Soya rejimi ${fmtDate(s.shadow_until)} gacha: kotib javoblarni jim tayyorlab, siz yozganingiz bilan solishtiradi. Hech narsa yuborilmaydi va qoralama ham chiqmaydi.`),
          h('button', { class: 'btn ghost sm', onclick: busy(async () => {
            const r = await api('POST', '/style/shadow', { days: 0 });
            s.shadow_until = r.shadow_until; draw();
          }) }, "To'xtatish"))
        : h('button', { class: 'btn ghost block', onclick: busy(async () => {
          const ok = await confirmBox("Soya rejimi 7 kun: kotib hech narsa yubormaydi va qoralama ham ko'rsatmaydi — faqat siz qanday javob berishingizni kuzatib o'rganadi. Oxirida natija va avto rejim taklifi keladi. Boshlansinmi?");
          if (!ok) return;
          const r = await api('POST', '/style/shadow', { days: 7 });
          s.shadow_until = r.shadow_until; s.learn = r.learn; draw(); toast('Soya rejimi boshlandi');
        }) }, '👀 Soya rejimi (7 kun)'),
      field('Uslub profili', profile, "Ko'rib chiqing va xohlasangiz tahrirlang — kotib aynan shunga amal qiladi."));
  };
  draw();
  return card;
}

// ---------- Bilim bazasi ----------
function knowledgeCard(s) {
  const kb = h('textarea', { maxlength: 4000, placeholder: "Masalan:\nIsh vaqti: 9:00–18:00, yakshanba dam\nYetkazib berish: shahar ichida bepul, 1 kunda\nNarxlar: konsultatsiya — 200 000 so'm" }, s.knowledge || '');
  kb.addEventListener('input', () => { s.knowledge = kb.value; markDirty(); });
  return h('div', { class: 'card' },
    h('p', { class: 'muted small' }, "Ko'p so'raladigan savollarga javoblar: narxlar, ish vaqti, xizmatlar, manzil. Kotib faqat shu ma'lumotdan foydalanadi, bazada yo'q narsani o'ylab topmaydi. Do'kon va biznes akkauntlari uchun qulay."),
    kb);
}

// ---------- O'zim haqimda ----------
function aboutCard(s) {
  const ta = h('textarea', { maxlength: 2000, rows: 6, placeholder: "Masalan:\nIsmim Alisher, 34 yoshdaman, Toshkentda yashayman.\nDasturchiman, «X» kompaniyasida ishlayman.\nTurmush o'rtog'im va 2 farzandim bor, qizim 5-maktabda o'qiydi.\nDo'stlarimga «sen», kattalarga «siz» deb yozaman." }, s.about || '');
  const counter = h('div', { class: 'counter' });
  const upd = () => { counter.textContent = ta.value.length + ' / 2000'; };
  ta.addEventListener('input', () => { s.about = ta.value; upd(); markDirty(); }); upd();
  return h('div', { class: 'card' },
    h('p', { class: 'muted small' }, "Kotib siz haqingizda qancha ko'p bilsa, «keyinroq yozaman» o'rniga shuncha mazmunli javob yozadi. Kasbingiz, oilangiz, odatlaringiz, kimga qanday murojaat qilishingiz — xohlaganingizni yozing."),
    ta, counter);
}

// ---------- Kun tartibi ----------
function routineCard(s) {
  s.routine = s.routine || [];
  const list = h('div', {});
  const draw = () => swap(list, s.routine.map((r, i) => {
    r.days = r.days || [];
    const days = h('div', { class: 'chips' }, WEEK.map(([d, label]) => h('button', { class: 'chip-btn' + (r.days.includes(d) ? ' on' : ''),
      onclick: (e) => { r.days = r.days.includes(d) ? r.days.filter((x) => x !== d) : [...r.days, d]; e.currentTarget.classList.toggle('on'); haptic('tap'); markDirty(); } }, label)));
    return h('div', { class: 'card soft' },
      h('div', { class: 'row nowrap' },
        h('input', { type: 'text', class: 'grow', maxlength: 100, value: r.activity || '', placeholder: 'Nima qilasiz? Masalan: ishda, darsda, sportzalda',
          oninput: (e) => { r.activity = e.target.value; markDirty(); } }),
        h('button', { class: 'btn danger-ghost sm', onclick: () => { s.routine.splice(i, 1); markDirty(); draw(); } }, '✖️')),
      h('div', { class: 'two' },
        field('Boshlanishi', h('input', { type: 'time', value: r.start, onchange: (e) => { r.start = e.target.value; markDirty(); } })),
        field('Tugashi', h('input', { type: 'time', value: r.end, onchange: (e) => { r.end = e.target.value; markDirty(); } }))),
      field('Kunlar', days, 'Hech biri tanlanmasa — har kuni.'));
  }));
  draw();
  return h('div', { class: 'card' },
    h('p', { class: 'muted small' }, "Qaysi kun, qaysi soatda nima qilishingizni kiriting. Kotib hozirgi vaqtga qarab band yoki bo'shligingizni aniqlaydi va kerak bo'lsa «hozir darsdaman, 15:00 dan keyin yozaman» kabi javob beradi. Vaqtlar Tinch soatlardagi vaqt mintaqasida."),
    list,
    h('button', { class: 'btn ghost sm', onclick: () => {
      if (s.routine.length >= 40) { toast("40 tadan ko'p bo'lmaydi"); return; }
      s.routine.push({ days: [1, 2, 3, 4, 5], start: '09:00', end: '18:00', activity: '' }); markDirty(); draw();
    } }, "➕ Qo'shish"));
}

// ---------- Kontakt eslatmalari ----------
function notesCard() {
  const s = state.settings;
  s.contact_notes = s.contact_notes || {};
  const names = new Map((state.contacts || []).map((c) => [String(c.id), c.name]));
  const list = h('div', {});
  const pick = select([['', 'Kontaktni tanlang…'], ...(state.contacts || []).filter((c) => !s.contact_notes[c.id]).map((c) => [String(c.id), c.name])], '', () => {});
  const text = h('input', { type: 'text', class: 'grow', maxlength: 300, placeholder: "Masalan: onam / loyiha X bo'yicha hamkor, «siz» deb yoz" });
  const draw = () => swap(list, ...Object.entries(s.contact_notes).map(([id, note]) => {
    const input = h('input', { type: 'text', class: 'grow', value: note, maxlength: 300, oninput: (e) => { s.contact_notes[id] = e.target.value; markDirty(); } });
    return field(names.get(id) || ('#' + id), h('div', { class: 'row' }, input,
      h('button', { class: 'btn danger-ghost sm', onclick: () => { delete s.contact_notes[id]; markDirty(); draw(); } }, '✖️')));
  }));
  draw();
  return h('div', { class: 'card' },
    h('p', { class: 'muted small' }, "Kotib shu kontaktga javob yozganda eslatmangizni hisobga oladi (kim ekani, qanday murojaat qilish)."),
    list,
    h('div', { class: 'row' }, pick, text,
      h('button', { class: 'btn sm', onclick: () => {
        const id = pick.value; const v = text.value.trim();
        if (!id || !v) return toast('Kontakt va eslatmani kiriting', true);
        s.contact_notes[id] = v; text.value = ''; markDirty(); draw(); haptic('tap');
      } }, "Qo'shish")));
}

// ---------- Qoidalar ----------
async function viewRules(view) {
  const s = state.settings;
  put(view, h('h1', {}, 'Qoidalar'),
    h('p', { class: 'muted' }, 'Tanlangan kontaktlar uchun alohida uslub yoki rejim. Ro\'yxatda birinchi mos kelgan qoida ishlaydi.'));
  if (state.contacts === null) {
    const sk = h('div', { class: 'skeleton' });
    put(view, sk);
    try { state.contacts = await api('GET', '/contacts'); }
    catch (e) { state.contacts = []; put(view, h('p', { class: 'err small' }, 'Kontaktlar yuklanmadi: ' + e.message)); }
    sk.remove();
    if (state.tab !== 'rules') return;
  }
  const list = h('div', {});
  const redraw = () => swap(list, ...(s.rules.length ? s.rules.map((r, i) => ruleCard(r, i, redraw))
    : [h('div', { class: 'empty' }, h('div', { class: 'big' }, "Qoidalar yo'q"), h('p', { class: 'muted' }, 'Hamma kontaktlarga umumiy sozlama qo\'llanadi.'))]));
  redraw();
  put(view, list,
    h('button', { class: 'btn ghost block', onclick: () => {
      if (s.rules.length >= 50) return toast('Qoidalar soni cheklangan', true);
      s.rules.push({ id: rid(), title: 'Yangi qoida', match: { user_ids: [] }, persona: 'neutral' });
      markDirty(); redraw(); haptic('tap');
    } }, '+ Qoida qo\'shish'),
    h('h2', {}, 'Kontakt eslatmalari'),
    notesCard(),
    h('div', { class: 'field' }, saveButton()));
}

function ruleCard(r, i, redraw) {
  const search = h('input', { type: 'search', placeholder: 'Kontakt qidirish…' });
  const box = h('div', { class: 'clist' });
  const chips = h('div', { class: 'chips' });
  const names = new Map(state.contacts.map((c) => [c.id, c.name]));
  const drawChips = () => swap(chips, ...(r.match.user_ids || []).map((id) => h('span', { class: 'chip' }, names.get(id) || ('#' + id))));
  const drawContacts = () => {
    const q = search.value.trim().toLowerCase();
    const items = state.contacts.filter((c) => !q || c.name.toLowerCase().includes(q)).slice(0, 200);
    swap(box, ...(items.length ? items.map((c) =>
      h('label', {}, h('input', { type: 'checkbox', checked: (r.match.user_ids || []).includes(c.id), onchange: (e) => {
        const set = new Set(r.match.user_ids || []);
        if (e.target.checked) set.add(c.id); else set.delete(c.id);
        r.match.user_ids = [...set]; markDirty(); drawChips();
      } }), c.name))
      : [h('label', { class: 'muted' }, state.contacts.length ? 'Topilmadi' : 'Kontaktlar mavjud emas (sessiya ulanmagan)')]));
  };
  search.addEventListener('input', drawContacts);
  drawContacts(); drawChips();

  const custom = h('textarea', { maxlength: 600, placeholder: "O'z ko'rsatmangiz" }, r.custom || '');
  custom.addEventListener('input', () => { r.custom = custom.value; markDirty(); });

  return h('div', { class: 'card' },
    h('div', { class: 'card-head' },
      h('input', { type: 'text', value: r.title || '', maxlength: 60, 'aria-label': 'Qoida nomi', oninput: (e) => { r.title = e.target.value; markDirty(); } })),
    field('Kontaktlar', h('div', { class: 'full' }, search, box, chips)),
    field('Uslub', personaSelect(r.persona || 'neutral', (v) => { r.persona = v; })),
    field("O'z ko'rsatma", custom),
    field('Rejim', select([['', 'Umumiy rejim'], ['draft', 'Qoralama'], ['auto', 'Avtomatik'], ['off', 'Javob yozma']], r.mode || '', (v) => { r.mode = v || undefined; })),
    switchRow("Vaqtincha o'chirish", null, r.disabled, (v) => { r.disabled = v; }),
    h('div', { class: 'row end' }, h('button', { class: 'btn danger-ghost sm', onclick: () => {
      state.settings.rules.splice(i, 1); markDirty(); redraw(); haptic('warning');
    } }, "Qoidani o'chirish")));
}

// ---------- Hisob: obuna, faollik, xavfsizlik ----------
// ---------- Avtomatlashtirish (iPhone Shortcuts / Android MacroDroid) ----------
// Tayyor holatlar: [belgi, nom, kotib javobda hisobga oladigan matn, standart muddat (daqiqa; 0 — muddatsiz),
// avtomatika havolasida qo'shimcha rejim (bo'lmasa rejim o'zgarmaydi)].
const STATUS_PRESETS = [
  ['🚗', 'Rulda', 'Hozir rulda', 60, 'auto'],
  ['📅', "Yig'ilishda", "Yig'ilishdaman", 60, 'auto'],
  ['🌙', 'Uxlayapti', 'Uxlayapman', 480, 'draft'],
  ['🛋', 'Dam olmoqda', 'Dam olyapman', 120],
  ['🚶', 'Sayrda', 'Sayr qilyapman', 60],
  ['🎧', 'Musiqa tinglamoqda', 'Musiqa tinglayapman, telefonga kam qarayapman', 60],
  ['📚', 'Dars qilmoqda', 'Dars qilyapman', 90],
  ['💻', 'Dastur tuzmoqda', 'Dastur yozyapman, diqqatim ishda', 120],
  ['💼', 'Ishda band', 'Ishda bandman', 120],
  ['🏋️', 'Sportda', 'Sport bilan shug\'ullanyapman', 90],
  ['🕌', 'Namozda', 'Namozdaman', 20],
  ['🍽', 'Ovqatlanmoqda', 'Ovqatlanyapman', 30],
  ['👨‍👩‍👧', 'Oila bilan', 'Oilam bilanman', 120],
  ['✈️', 'Safarda', 'Safardaman, aloqa yomon bo\'lishi mumkin', 0],
  ['🤒', 'Betob', 'Biroz betobman', 0],
];

const HOOK_PRESETS = [
  ...STATUS_PRESETS.map(([icon, label, away, , mode]) => [`${icon} ${label}`, mode ? { mode, away } : { away }]),
  ['↩️ Odatiy holat', { mode: 'draft', away: '' }],
  ["⏸ To'xtatish", { action: 'pause' }],
  ['▶️ Davom ettirish', { action: 'resume' }],
];

// Amaldagi holat (muddati o'tgan bo'lsa — yo'q).
function currentAway(s) {
  if (!s || !s.away) return '';
  if (s.away_until && new Date(s.away_until) <= new Date()) return '';
  return s.away;
}

function fmtTime(iso) {
  const d = new Date(iso);
  const sameDay = d.toDateString() === new Date().toDateString();
  const hm = d.toLocaleTimeString('uz-UZ', { hour: '2-digit', minute: '2-digit', hour12: false });
  return sameDay ? hm : `${fmtDate(iso)} ${hm}`;
}

function fmtMinutes(m) {
  if (!m) return 'muddatsiz';
  if (m < 60) return `${m} daqiqa`;
  return m % 60 ? `${Math.floor(m / 60)} soat ${m % 60} daqiqa` : `${m / 60} soat`;
}

async function setAway(text, minutes) {
  const r = await api('POST', '/away', { away: text, minutes });
  state.settings.away = r.away || '';
  state.settings.away_until = r.away_until || null;
  haptic('success');
}

// Bosh sahifa: "Hozir nima qilyapsiz?" — bir bosishda holat (muddati bilan).
function awayCard() {
  const card = h('div', { class: 'card' });
  let picked = null;
  const draw = () => {
    const now = currentAway(state.settings);
    const head = h('div', { class: 'card-head' }, h('h3', {}, 'Hozir nima qilyapsiz?'),
      now ? h('span', { class: 'badge accent' }, 'Holat bor') : null);
    const kids = [head];
    if (now) {
      kids.push(h('p', { class: 'small' }, h('b', {}, now),
        state.settings.away_until ? h('span', { class: 'muted' }, ` · ${fmtTime(state.settings.away_until)} gacha`) : null));
    } else {
      kids.push(h('p', { class: 'muted small' }, 'Tanlang — kotib javoblarda hisobga oladi (masalan: «hozir rulda, keyinroq yozadi»). Vaqti tugagach o\'zi o\'chadi.'));
    }
    kids.push(h('div', { class: 'status-grid' }, STATUS_PRESETS.map((p) => {
      const [icon, label, away] = p;
      const on = now === away;
      return h('button', { class: 'status-btn' + (on ? ' on' : '') + (picked === p ? ' picked' : ''), onclick: () => { picked = picked === p ? null : p; haptic('tap'); draw(); } },
        h('span', { class: 'ico' }, icon), h('span', {}, label));
    })));
    if (picked) {
      const [icon, label, away, def] = picked;
      const opts = [...new Set([def, 30, 60, 120, 240, 0])];
      kids.push(h('p', { class: 'small' }, `${icon} ${label} — qancha vaqt?`),
        h('div', { class: 'chips' }, opts.map((m) => h('button', { class: 'chip-btn' + (m === def ? ' on' : ''), onclick: busy(async () => {
          await setAway(away, m); picked = null; toast(`Holat: ${label}`); draw();
        }) }, fmtMinutes(m)))));
    }
    if (now) {
      kids.push(h('button', { class: 'btn ghost block', onclick: busy(async () => { await setAway('', 0); picked = null; toast('Holat tozalandi'); draw(); }) }, 'Holatni tozalash'));
    }
    swap(card, kids);
  };
  draw();
  return card;
}

function hookURL(base, params) {
  return base + '?' + Object.entries(params).map(([k, v]) => k + '=' + encodeURIComponent(v)).join('&');
}

async function copyText(text) {
  try { await navigator.clipboard.writeText(text); toast('Nusxa olindi'); haptic('success'); return; } catch (_) { /* eski WebView */ }
  // Zaxira: tanlab qo'yamiz — foydalanuvchi o'zi nusxalaydi.
  const ta = h('textarea', { readonly: true }, text);
  document.body.append(ta); ta.select();
  try { document.execCommand('copy'); toast('Nusxa olindi'); } catch (_) { toast('Matnni belgilab nusxa oling', true); }
  ta.remove();
}

function botLink(start) {
  const u = state.me.bot_username;
  return u ? `t.me/${u}?start=${start}` : `t.me/<bot>?start=${start}`;
}

function hookCard() {
  const card = h('div', { class: 'card' });
  const draw = () => {
    const head = h('div', { class: 'card-head' }, h('h3', {}, 'Avtomatlashtirish'),
      h('span', { class: 'badge ' + (state.me.hook ? 'ok' : '') }, state.me.hook ? 'Yoqilgan' : "O'chiq"));
    const create = h('button', { class: state.me.hook ? 'btn ghost block' : 'btn block', onclick: busy(async () => {
      if (state.me.hook && !(await confirmBox("Yangi havola yaratilsa, eskisi ishlamay qoladi (telefondagi avtomatikani yangilash kerak). Davom etasizmi?"))) return;
      const { url } = await api('POST', '/hook');
      state.hookURL = url; state.me.hook = true; draw();
    }) }, state.me.hook ? 'Yangi havola yaratish' : 'Havola yaratish');
    const kids = [head,
      h('p', { class: 'muted small' }, "Telefon rejimiga qarab kotibni o'zi boshqaradi: iPhone'da Focus (Haydash, Uyqu), Android'da MacroDroid/Tasker (Bezovta qilmang, mashina Bluetooth'i)."),
    ];
    if (state.hookURL) {
      kids.push(h('p', { class: 'small' }, 'Kerakli havolani nusxalab, telefon avtomatikasiga qo\'ying:'),
        (() => {
          const sel = h('select', {}, HOOK_PRESETS.map(([label], i) => h('option', { value: String(i) }, label)));
          return h('div', { class: 'hook-list' }, sel,
            h('button', { class: 'btn ghost block', onclick: () => copyText(hookURL(state.hookURL, HOOK_PRESETS[+sel.value][1])) }, 'Tanlangan havoladan nusxa olish'));
        })(),
        h('p', { class: 'muted small' }, "Holat o'zi tugashi uchun havola oxiriga &for=60 qo'shing (daqiqa)."),
        h('p', { class: 'muted small' }, "Havolani hech kimga bermang: u orqali kotib rejimini o'zgartirish mumkin."));
    } else if (state.me.hook) {
      kids.push(h('p', { class: 'muted small' }, "Havola xavfsizlik uchun faqat yaratilganda ko'rsatiladi. Qayta ko'rish uchun yangisini yarating."));
    }
    kids.push(create);
    if (state.me.hook) {
      kids.push(h('button', { class: 'btn ghost block', onclick: busy(async () => {
        await api('DELETE', '/hook'); state.me.hook = false; state.hookURL = null; toast("O'chirildi"); draw();
      }) }, "Havolani o'chirish"));
    }
    kids.push(h('details', {},
      h('summary', { class: 'small' }, "iPhone: Shortcuts'da sozlash"),
      h('ol', { class: 'small muted' },
        h('li', {}, 'Shortcuts → Automation → «+» → Focus → masalan «Driving» → «When Turning On».'),
        h('li', {}, '«Run Immediately» ni tanlang → New Blank Automation → «Get Contents of URL».'),
        h('li', {}, '«🚗 Haydash» havolasini qo\'ying. Xuddi shunday «When Turning Off» uchun «↩️ Odatiy holat».'),
        h('li', {}, `Siri: «Hey Siri, kotibni to'xtat» — Shortcuts'da «Open URL» bilan ${botLink('pause')} (davom: ${botLink('resume')}).`))),
    h('details', {},
      h('summary', { class: 'small' }, "Android: MacroDroid'da sozlash"),
      h('ol', { class: 'small muted' },
        h('li', {}, 'MacroDroid → Add Macro → Trigger: «Do Not Disturb» yoki «Bluetooth Event» (mashina).'),
        h('li', {}, 'Action: Connectivity → «HTTP Request» → GET → havolani qo\'ying.'),
        h('li', {}, 'Teskari makro (DND o\'chganda) uchun «↩️ Odatiy holat» havolasi.'),
        h('li', {}, `Google Assistant / bosh ekran yorlig'i: ${botLink('pause')} va ${botLink('resume')}.`))));
    swap(card, kids);
  };
  draw();
  return card;
}

// ---------- Bosh ekranga qo'shish ----------
// telegram-web-app.js addToHomeScreen'ni har doim e'lon qiladi, lekin Telegram 8.0 dan eski
// ilovada chaqiruv jimgina hech narsa qilmaydi — shuning uchun versiya va holatni tekshiramiz,
// qo'llanmasa qo'lda qo'shish yo'riqnomasini ko'rsatamiz.
function homeScreenSupported() {
  try { return !!(tg && tg.addToHomeScreen && tg.isVersionAtLeast && tg.isVersionAtLeast('8.0')); } catch (_) { return false; }
}

function isIOS() {
  return (tg && tg.platform === 'ios') || /iPhone|iPad|iPod/.test(navigator.userAgent);
}

function homeScreenManual() {
  const link = state.me.bot_username ? `https://t.me/${state.me.bot_username}?startapp` : null;
  const steps = isIOS()
    ? [
      h('li', {}, link ? ["Safari'da oching: ", h('b', {}, link)] : "Safari'da botingizning t.me/<bot>?startapp havolasini oching."),
      h('li', {}, '«Ulashish» (□↑) → «Bosh ekranga» (Add to Home Screen) → «Qo\'shish».'),
      h('li', {}, 'Belgi bosilganda Telegram kotib ilovasi bilan ochiladi.')]
    : [
      h('li', {}, 'Telegram\'da bot bilan chatni oching.'),
      h('li', {}, 'Yuqoridagi ⋮ menyu → «Bosh ekranga qo\'shish» (Add to Home Screen / Ярлык).'),
      h('li', {}, "Yoki Telegram'ni yangilang — shunda pastdagi tugma ishlaydi."),
      h('li', {}, "Baribir qo'shilmasa: telefon Sozlamalari → Ilovalar → Telegram → Ruxsatlar → «Bosh ekranga yorliqlar» ni yoqing (Xiaomi, Huawei, Oppo, Vivo'da ko'pincha o'chiq).")];
  const kids = [h('ol', { class: 'small muted' }, steps)];
  if (link) kids.push(h('button', { class: 'btn ghost block', onclick: () => copyText(link) }, 'Havolani nusxalash'));
  return kids;
}

function homeScreenBlock() {
  const box = h('div', {});
  const intro = h('p', { class: 'muted small' }, "Kotib telefoningiz bosh ekranida alohida ilova bo'lib turadi.");
  const manual = (why) => swap(box, intro, why ? h('p', { class: 'small' }, why) : null, homeScreenManual());
  if (!homeScreenSupported()) {
    manual("Telegram ilovangiz bu funksiyani qo'llamaydi (Telegram'ni yangilang). Qo'lda qo'shish:");
    return box;
  }
  const btn = h('button', { class: 'btn ghost block', onclick: () => {
    let done = false;
    const onAdded = () => { done = true; toast("Bosh ekranga qo'shildi ✅"); haptic('success'); swap(box, intro, h('p', { class: 'small' }, "✅ Bosh ekranga qo'shilgan.")); };
    try { tg.onEvent('homeScreenAdded', onAdded); } catch (_) { /* */ }
    try { tg.addToHomeScreen(); } catch (_) { manual("Bu qurilmada avtomatik qo'shib bo'lmadi. Qo'lda qo'shish:"); return; }
    // Ba'zi qurilmalar natijani xabar qilmaydi yoki Telegram'ga yorliq ruxsati berilmagan:
    // oyna chiqmagan bo'lsa, qo'lda yo'l (ruxsatni tekshirish bilan) ko'rsatamiz.
    setTimeout(() => { if (!done && !document.hidden) manual("Oyna chiqmadimi? Qo'lda qo'shish:"); }, 4000);
  } }, "Bosh ekranga qo'shish");
  swap(box, intro, btn);
  try {
    tg.checkHomeScreenStatus((st) => {
      if (st === 'added') swap(box, intro, h('p', { class: 'small' }, "✅ Bosh ekranga qo'shilgan."));
      else if (st === 'unsupported') manual("Bu qurilmada avtomatik qo'shib bo'lmaydi. Qo'lda qo'shish:");
    });
  } catch (_) { /* */ }
  return box;
}

// deviceCard — telefon qulayliklari: biometrik himoya va bosh ekranga qo'shish.
function deviceCard() {
  const rows = [];
  if (bio.ready && bio.available) {
    rows.push(switchRow(bioName(), "Qoralama yuborish, avtomatik rejimni yoqish va akkauntni o'chirishdan oldin so'raladi (shu telefonda).",
      bioEnabled(), async (v) => {
        if (v && !(await bioRequestAccess())) { bioSet(false); toast('Ruxsat berilmadi', true); renderMain(); return; }
        bioSet(v); toast(v ? 'Himoya yoqildi' : "Himoya o'chirildi");
      }, true));
  }
  rows.push(homeScreenBlock());
  if (!rows.length) return null;
  return h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, 'Telefon')), rows);
}

function googleCard(g) {
  const status = h('span', { class: 'badge ' + (g.connected ? 'ok' : '') }, g.connected ? 'Ulangan' : 'Ulanmagan');
  const body = g.connected
    ? [
      h('p', { class: 'muted small' }, g.email ? `Akkaunt: ${g.email}` : 'Google akkaunt ulangan.'),
      h('ul', { class: 'perks' },
        h('li', {}, "Kotib taqvimdagi band vaqtlaringizni biladi"),
        h('li', {}, "Uchrashuv va vazifalarni botda tasdiqlasangiz — Calendar/Tasks'ga qo'shiladi"),
        h('li', {}, "Kontakt haqidagi eslatmalaringiz (Google Contacts) hisobga olinadi")),
      h('button', { class: 'btn ghost block', onclick: busy(async () => {
        if (!(await confirmBox("Google uziladi va ruxsat bekor qilinadi. Davom etasizmi?"))) return;
        await bioGate("Google'ni uzish");
        await api('POST', '/google/disconnect'); await refreshMe(); toast('Google uzildi'); renderMain();
      }) }, 'Google\'ni uzish'),
    ]
    : [
      h('p', { class: 'muted small' }, "Calendar, Contacts va Tasks. Kotib hech narsani o'zi qo'shmaydi — har bir uchrashuv va vazifa uchun botda sizdan so'raydi."),
      h('button', { class: 'btn block', onclick: busy(async () => {
        const { url } = await api('POST', '/google/connect');
        // Google ichki brauzerda (WebView) kirishni bloklaydi — tashqi brauzerda ochamiz.
        if (tg && tg.openLink) tg.openLink(url); else window.open(url, '_blank', 'noopener');
        toast("Brauzerda ruxsat bering, so'ng shu yerga qayting");
      }) }, "Google'ni ulash"),
    ];
  return h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, 'Google'), status), body);
}

async function viewAccount(view) {
  put(view, h('h1', {}, isChild() ? `Hisob · 👤 ${accountLabel()}` : 'Hisob'));
  const b = state.me.billing || {};
  if (isChild()) {
    put(view, h('div', { class: 'card soft' }, h('p', { class: 'small muted' }, "Bu qo'shimcha akkaunt: obuna va do'st taklifi asosiy akkauntda boshqariladi.")));
  } else {
    if (b.enabled) put(view, subscriptionCard(b));
    put(view, accountsCard());
    if (b.enabled && !b.exempt) put(view, referralCard());
    put(view, charityCard());
  }
  const g = state.me.google || {};
  if (g.enabled) put(view, needsPro() && !g.connected ? proLock('Google', "Kotib taqvimingizdagi band vaqtlarni biladi, uchrashuv va vazifalarni Google Calendar/Tasks'ga qo'shadi.") : googleCard(g));
  const dev = deviceCard();
  if (dev) put(view, dev);
  put(view, needsPro() ? proLock('Avtomatlashtirish', "Telefon rejimiga qarab (Focus, Bezovta qilmang, mashina Bluetooth'i) kotib holati o'zi o'zgaradi.") : hookCard());

  put(view, h('h2', {}, 'Oxirgi faollik'));
  const act = h('div', { class: 'card' }, h('div', { class: 'skeleton' }));
  put(view, act);

  put(view, 
    h('h2', {}, 'Xavfsizlik'),
    h('div', { class: 'card soft' },
      h('ul', { class: 'perks' },
        h('li', {}, 'Sessiya AES-256 bilan shifrlangan, kalit bazada saqlanmaydi'),
        h('li', {}, 'Kod va 2FA parol saqlanmaydi, logga yozilmaydi'),
        h('li', {}, "Faollik jurnalida xabar matnlari yo'q — faqat hodisalar"))),
    h('div', { class: 'card danger' },
      h('h3', {}, isChild() ? "Bu akkauntni uzish" : "Akkauntni uzish va o'chirish"),
      h('p', { class: 'muted small' }, isChild()
        ? "Faqat shu qo'shimcha akkaunt uziladi: Telegram sessiyasi tugatiladi, uning sozlama va qoralamalari o'chadi. Asosiy akkaunt va obuna tegilmaydi."
        : "Telegram sessiyasi tugatiladi, sozlamalar va qoralamalar o'chadi, obuna yangilanishi to'xtatiladi. Qo'shimcha akkauntlar ham uziladi."),
      h('button', { class: 'btn danger block', onclick: busy(async () => {
        if (!(await confirmBox(isChild() ? `«${accountLabel()}» uziladi. Davom etasizmi?` : "Hamma narsa o'chiriladi. Bu amalni ortga qaytarib bo'lmaydi. Davom etasizmi?"))) return;
        await bioGate("Akkauntni o'chirish");
        await api('DELETE', '/account', { confirm: 'DELETE' });
        toast(isChild() ? 'Uzildi' : "O'chirildi");
        if (isChild()) { await switchAccount(state.me.main_id); return; }
        setTimeout(() => { if (tg) tg.close(); }, 800);
      }) }, isChild() ? 'Uzish' : "Uzish va o'chirish")));

  try { state.activity = await api('GET', '/activity'); } catch (e) { swap(act, h('p', { class: 'err small' }, e.message)); return; }
  swap(act, state.activity.length
    ? h('ul', { class: 'timeline' }, state.activity.slice(0, 20).map((e) =>
      h('li', {}, h('i', { class: 'dot' }), h('div', {},
        h('div', {}, EVENTS[e.event] || e.event, e.event === 'skip' && e.detail ? h('span', { class: 'muted small' }, ' · ' + e.detail) : null),
        h('div', { class: 'when' }, fmtAgo(e.at))))))
    : h('p', { class: 'muted' }, "Hozircha faollik yo'q."));
}

function planCard(b, tier, title, perks, opts) {
  const price = tier === 'pro' ? b.pro_price : b.price;
  const current = b.paid && b.tier === tier;
  return h('div', { class: 'plan' + (tier === 'pro' ? ' pro' : '') + (current ? ' current' : '') },
    h('div', { class: 'card-head' }, h('h3', {}, title), current ? h('span', { class: 'badge ok' }, 'Hozirgi') : (tier === 'pro' ? h('span', { class: 'badge accent' }, 'Tavsiya') : null)),
    h('div', { class: 'price' }, String(price) + ' ⭐ ', h('small', {}, '/ 30 kun')),
    h('ul', { class: 'perks' }, perks.map((p) => h('li', {}, p))),
    current ? null : h('button', { class: 'btn block' + (tier === 'pro' ? '' : ' ghost'), onclick: busy(async () => {
      if (opts.switching && !(await confirmBox(`${TIER_NAMES[tier]} tarifiga o'tasizmi? Hozirgi obunangiz yangilanmaydi, qolgan kunlari narx nisbatida yangi tarifga bonus kun sifatida qo'shiladi.`))) return;
      await pay(tier);
    }) }, opts.switching ? `${TIER_NAMES[tier]}'ga o'tish · ${price} ⭐` : `${TIER_NAMES[tier]} · ${price} ⭐`));
}

function plansBlock(b, switching) {
  return h('div', { class: 'plans' }, PLANS.map(([tier, title, perks]) => planCard(b, tier, title, perks, { switching })));
}

function subscriptionCard(b) {
  if (b.exempt) {
    return h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, '👑 Admin obunasi'), h('span', { class: 'badge ok' }, 'Faol · muddatsiz')),
      h('ul', { class: 'perks' },
        h('li', {}, 'Barcha Pro funksiyalari ochiq'),
        h('li', {}, 'Kunlik limit va to\'lov yo\'q'),
        h('li', {}, "Faqat ADMIN_IDS ro'yxatidagilar uchun — sotib olinmaydi, boshqaga berilmaydi")),
      state.me.admin ? h('button', { class: 'btn ghost block', onclick: () => openAdmin() }, '👑 Admin paneli') : null);
  }
  const total = b.paid ? 30 : Math.max(1, b.trial_days || 1);
  const pct = Math.max(0, Math.min(100, (b.days_left / total) * 100));
  let status, body;
  if (b.paid) {
    status = h('span', { class: 'badge ' + (b.canceled ? 'warn' : 'ok') }, (b.canceled ? 'Yangilanmaydi' : 'Faol') + tierLabel(b));
    body = [
      h('p', { class: 'muted small' }, b.canceled ? `${fmtDate(b.paid_until)} gacha amal qiladi, keyin to'xtaydi.` : `Keyingi to'lov: ${fmtDate(b.paid_until)} · ${b.tier === 'pro' && b.pro_price ? b.pro_price : b.price} ⭐`),
      h('div', { class: 'progress' }, h('i', { style: null, 'data-w': pct })),
      b.bonus_until ? h('p', { class: 'muted small' }, `🎁 Bonus kunlaringiz to'langan muddatdan keyin ishlatiladi (${fmtDate(b.bonus_until)} gacha). Obuna yangilansa, ular keyinga suriladi — yo'qolmaydi.`) : null,
      b.renewable
        ? h('button', { class: 'btn block', onclick: busy(async () => { await api('POST', '/billing/renew'); await refreshMe(); toast('Obuna qayta yoqildi'); renderMain(); }) }, 'Obunani qayta yoqish')
        : h('button', { class: 'btn ghost block', onclick: busy(async () => {
          if (!(await confirmBox(`Obuna ${fmtDate(b.paid_until)} gacha ishlaydi, keyin yangilanmaydi. Bekor qilinsinmi?`))) return;
          await api('POST', '/billing/cancel'); await refreshMe(); toast('Obuna bekor qilindi'); renderMain();
        }) }, 'Obunani bekor qilish'),
      ...(tiered(b) ? [h('p', { class: 'small' }, b.tier === 'pro' ? 'Tariflar' : "🚀 Pro'da ovozli xabarlar, Google va telefon avtomatikasi bor:"), plansBlock(b, true)] : []),
    ];
  } else {
    status = b.bonus
      ? h('span', { class: 'badge ok' }, `Bonus · ${b.days_left} kun`)
      : h('span', { class: 'badge ' + (b.on_trial ? 'accent' : 'bad') }, b.on_trial ? `Sinov · ${b.days_left} kun` : 'Faol emas');
    body = [
      b.bonus ? h('p', { class: 'small' }, `🎁 Bonus (sovg'a yoki do'st taklifi): ${fmtDate(b.bonus_until)} gacha cheklovsiz ishlaydi. Keyin davom ettirish uchun obuna bo'ling.`) : null,
      b.on_trial && tiered(b) ? h('p', { class: 'small muted' }, "Sinov davrida Pro funksiyalari ham ochiq. Tugagach, tarifni tanlang:") : null,
      ...(tiered(b) ? [
        b.on_trial ? h('div', { class: 'progress' }, h('i', { 'data-w': pct })) : null,
        b.on_trial && b.trial_daily_limit ? h('p', { class: 'muted small' }, `Sinovda kuniga ${b.trial_daily_limit} tagacha javob tayyorlanadi.`) : null,
        plansBlock(b, false)] : [
      h('div', { class: 'price' }, String(b.price) + ' ⭐ ', h('small', {}, '/ 30 kun')),
      h('ul', { class: 'perks' },
        h('li', {}, 'Cheksiz javob qoralamalari'),
        h('li', {}, 'Shaxsiy qoidalar va uslublar'),
        h('li', {}, 'Har oy avtomatik yangilanadi, istalgan payt bekor qilinadi')),
      b.on_trial ? h('div', { class: 'progress' }, h('i', { 'data-w': pct })) : null,
      b.on_trial && b.trial_daily_limit ? h('p', { class: 'muted small' }, `Sinovda kuniga ${b.trial_daily_limit} tagacha javob tayyorlanadi.`) : null,
      h('button', { class: 'btn block', onclick: busy(() => pay('advanced')) }, `Obuna bo'lish · ${b.price} ⭐`)]),
      h('p', { class: 'muted small center' }, "To'lov Telegram Stars orqali — karta ma'lumotlari bizga kelmaydi."),
      h('p', { class: 'muted small center' }, "Maslahat: Stars ilova do'koni (App Store / Google Play) orqali qimmatroq. ",
        h('a', { href: '#', onclick: (e) => { e.preventDefault(); if (tg && tg.openTelegramLink) tg.openTelegramLink('https://t.me/PremiumBot'); } }, '@PremiumBot'),
        " orqali odatda arzonroq."),
    ];
  }
  const card = h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, 'Obuna'), status), body);
  // progress kengligi: inline style CSP'da taqiqlangan, shuning uchun CSSOM orqali.
  card.querySelectorAll('[data-w]').forEach((el) => { el.style.width = el.getAttribute('data-w') + '%'; });
  return card;
}

// ---------- Kengaytirilgan statistika (Pro) ----------
const MONTHS = ['yan', 'fev', 'mar', 'apr', 'may', 'iyun', 'iyul', 'avg', 'sen', 'okt', 'noy', 'dek'];
function fmtDay(d) { const [, m, day] = d.split('-'); return `${+day}-${MONTHS[+m - 1]}`; }

function kpi(value, label, cur, prev) {
  let delta = null;
  if (prev != null && prev > 0) {
    const pct = Math.round((cur - prev) * 100 / prev);
    delta = h('div', { class: 'd ' + (pct > 0 ? 'up' : pct < 0 ? 'down' : '') }, `${pct > 0 ? '▲ +' : pct < 0 ? '▼ ' : ''}${pct}% o'tgan davrga nisbatan`);
  }
  return h('div', { class: 'kpi' }, h('div', { class: 'v' }, value), h('div', { class: 'l' }, label), delta);
}

// Bitta seriyali ustunli grafik: bosilganda/ustiga kelganda qiymat yuqorida ko'rinadi.
function columnChart(points, unit, xLabels) {
  const max = Math.max(1, ...points.map((p) => p.v));
  const readout = h('div', { class: 'readout' }, 'Ustunni bosing — qiymat shu yerda');
  const cols = points.map((p) => {
    const bar = h('div', { class: 'bar', 'data-h': p.v ? Math.max(3, (p.v / max) * 100) : 0 });
    const col = h('div', { class: 'col', role: 'img', 'aria-label': `${p.label}: ${p.v} ${unit}`, title: `${p.label}: ${p.v} ${unit}` }, bar);
    const show = () => { cols.forEach((c) => c.classList.remove('sel')); col.classList.add('sel'); readout.textContent = `${p.label}: ${p.v} ${unit}`; };
    col.addEventListener('pointerenter', show); col.addEventListener('click', show);
    return col;
  });
  const el = h('div', { class: 'chart' }, readout, h('div', { class: 'ymax' }, String(max)), h('div', { class: 'plot' }, cols),
    h('div', { class: 'xaxis' }, xLabels.map((x) => h('span', {}, x))));
  el.querySelectorAll('[data-h]').forEach((b) => { b.style.height = b.getAttribute('data-h') + '%'; });
  return el;
}

async function viewStats(view) {
  put(view, h('h1', {}, 'Statistika'));
  if (needsPro()) {
    put(view, proLock('Kengaytirilgan statistika', "Kunlik dinamika, qoralamalarni tasdiqlash ulushi, kontaktlar qachon ko'p yozishi, eng faol suhbatdoshlar va har dushanba haftalik hisobot."));
    return;
  }
  const days = state.statsDays || 7;
  const seg = h('div', { class: 'segmented' }, [7, 30].map((d) => h('button', { class: d === days ? 'on' : '',
    onclick: () => { state.statsDays = d; renderMain(); } }, `${d} kun`)));
  const body = h('div', {}, h('div', { class: 'skeleton' }));
  put(view, seg, body);
  let s;
  try { s = await api('GET', '/stats?days=' + days); } catch (e) { swap(body, h('p', { class: 'err small' }, e.message)); return; }
  if (!s.prepared && !s.skipped && !s.blocked) {
    swap(body, h('div', { class: 'card soft' }, h('p', { class: 'muted' }, `Oxirgi ${days} kunda faollik yo'q. Kotib javob tayyorlay boshlagach, statistika shu yerda paydo bo'ladi.`)));
    return;
  }
  const series = s.series || [];
  const busiest = s.hours.indexOf(Math.max(...s.hours));
  swap(body,
    h('div', { class: 'kpis' },
      kpi(String(s.prepared), 'Tayyorlangan javoblar', s.prepared, s.prev_prepared),
      kpi(String(s.sent), 'Yuborilgan', s.sent, s.prev_sent),
      kpi(s.saved_min ? '~' + fmtMinutes(s.saved_min) : '0', 'Tejalgan vaqt'),
      kpi(s.approval >= 0 ? s.approval + '%' : '—', "Qoralamalarni tasdiqlash")),
    h('div', { class: 'card' },
      h('h3', {}, 'Kunlik tayyorlangan javoblar'),
      columnChart(series.map((d) => ({ label: fmtDay(d.day), v: d.prepared })), 'ta',
        [fmtDay(series[0].day), fmtDay(series[series.length - 1].day)])),
    h('div', { class: 'card' },
      h('h3', {}, 'Kontaktlar qachon yozadi'),
      h('p', { class: 'muted small' }, busiest >= 0 && s.hours[busiest] ? `Eng faol vaqt: ${String(busiest).padStart(2, '0')}:00–${String((busiest + 1) % 24).padStart(2, '0')}:00` : ''),
      columnChart(s.hours.map((v, i) => ({ label: `${String(i).padStart(2, '0')}:00`, v })), 'ta', ['00', '06', '12', '18', '23'])),
    s.top_chats && s.top_chats.length ? h('div', { class: 'card' },
      h('h3', {}, "Ko'p yozgan kontaktlar"),
      h('ul', { class: 'top-list' }, s.top_chats.map((c) => h('li', {}, h('span', {}, c.name), h('b', {}, `${c.count} ta`))))) : null,
    h('div', { class: 'card' },
      h('h3', {}, 'Tafsilot'),
      h('ul', { class: 'top-list' },
        h('li', {}, h('span', {}, '🤖 Avtomatik yuborilgan'), h('b', {}, String(s.auto_sent))),
        h('li', {}, h('span', {}, '✅ Qoralamadan yuborilgan'), h('b', {}, String(s.draft_sent))),
        h('li', {}, h('span', {}, '✖️ Rad etilgan qoralamalar'), h('b', {}, String(s.rejected))),
        h('li', {}, h('span', {}, "⏭ Javobsiz qoldirilgan (qoida, tinch soat, limit)"), h('b', {}, String(s.skipped))),
        h('li', {}, h('span', {}, '🛡 Xavfsizlik filtri'), h('b', {}, String(s.blocked)))),
      h('details', {}, h('summary', { class: 'small' }, "Kunlar bo'yicha jadval"),
        h('table', { class: 'stats-table' },
          h('thead', {}, h('tr', {}, h('th', {}, 'Kun'), h('th', {}, 'Tayyorlangan'), h('th', {}, 'Yuborilgan'))),
          h('tbody', {}, series.slice().reverse().map((d) => h('tr', {}, h('td', {}, fmtDay(d.day)), h('td', {}, String(d.prepared)), h('td', {}, String(d.sent)))))))),
    h('p', { class: 'muted small center' }, "Har dushanba soat 9:00 da haftalik hisobot botga keladi (Sozlamalarda o'chirish mumkin)."));
}

// ---------- Admin paneli (faqat ADMIN_IDS) ----------
const MODE_LABELS = { draft: 'Qoralama', auto: 'Avtomatik', shadow: 'Soya', off: "O'chiq" };
const SAFETY = { safe: ['Xavfsiz', 'ok'], medium: ["O'rta xavf", 'warn'], high: ['Yuqori xavf', 'bad'] };
const SIGNAL = { ok: ['Foydali', 'ok'], idle: ['Nofaol', ''], watch: ['Kuzatish kerak', 'warn'], suspicious: ['Shubhali', 'bad'] };
const ADMIN_FILTERS = [['all', 'Hammasi'], ['active', 'Faol'], ['auto', 'Avtomatik'], ['risky', 'Xavfli'], ['paid', "To'laganlar"],
  ['idle', 'Nofaol'], ['pending', 'Ulanmagan'], ['banned', 'Bloklangan']];
const GRANT_DAYS = [[7, '7 kun'], [30, '1 oy'], [90, '3 oy'], [365, '1 yil'], [3650, 'Muddatsiz']];
Object.assign(EVENTS, {
  admin_grant: "Admin sovg'a obuna berdi", admin_revoke_grant: "Admin sovg'ani bekor qildi", admin_ban: 'Admin blokladi',
  admin_unban: 'Admin blokni olib tashladi', admin_pause: "Admin to'xtatdi", admin_message: 'Admin xabar yubordi', admin_refund: "Admin to'lovni qaytardi",
});

function badge(map, key) { const [label, cls] = map[key] || [key || '—', '']; return h('span', { class: 'badge ' + cls }, label); }
function userTitle(u) { return u.name || u.owner_name || (u.username ? '@' + u.username : 'ID ' + u.id); }

function openAdmin(sub) {
  state.tab = 'admin'; state.sub = sub || null;
  haptic('tap'); window.scrollTo(0, 0); renderMain();
}

function adminEntry() {
  return h('div', { class: 'card accent' },
    h('div', { class: 'card-head' }, h('h3', {}, '👑 Admin paneli'), h('span', { class: 'badge accent' }, 'Admin')),
    h('p', { class: 'small' }, "Foydalanuvchilar, xavfsizlik rejimlari, shubhali faollik, sovg'a obuna, bloklash va to'lovlar."),
    h('button', { class: 'btn block', onclick: () => openAdmin() }, 'Panelni ochish'));
}

async function viewAdmin(view) {
  const tab = state.adminTab || 'overview';
  put(view, h('div', { class: 'card-head' }, h('h1', {}, '👑 Admin'), h('button', { class: 'btn ghost sm', onclick: () => go('home') }, '← Chiqish')),
    h('div', { class: 'segmented scroll' }, [['overview', 'Umumiy'], ['users', 'Foydalanuvchilar'], ['payments', "To'lovlar"], ['charity', 'Hayriya'], ['broadcast', 'Xabar']].map(([k, label]) =>
      h('button', { class: k === tab ? 'on' : '', onclick: () => { state.adminTab = k; haptic('tap'); renderMain(); } }, label))));
  const body = h('div', {}, h('div', { class: 'skeleton' }));
  put(view, body);
  try {
    await ({ overview: adminOverview, users: adminUsers, payments: adminPayments, charity: adminCharity, broadcast: adminBroadcast })[tab](body);
  } catch (e) { swap(body, h('p', { class: 'err' }, e.message)); }
}

// barList — nom → son ro'yxati, bosilsa foydalanuvchilar filtri ochiladi.
function barList(entries, total) {
  return h('ul', { class: 'bar-list' }, entries.map(([label, n, cls, filter]) => {
    const fill = h('i', { class: 'fill ' + (cls || ''), 'data-w': total ? Math.round((n / total) * 100) : 0 });
    const li = h('li', filter ? { class: 'link', onclick: () => { state.adminTab = 'users'; state.adminFilter = filter; state.adminQ = ''; renderMain(); } } : {},
      h('div', { class: 'row-between' }, h('span', {}, label), h('b', {}, String(n))), h('div', { class: 'track' }, fill));
    return li;
  }));
}
function applyWidths(el) { el.querySelectorAll('[data-w]').forEach((b) => { b.style.width = b.getAttribute('data-w') + '%'; }); }

async function adminOverview(body) {
  const o = await api('GET', '/admin/overview');
  const st = o.status || {}, md = o.modes || {}, sf = o.safety || {}, sg = o.signals || {}, ev = o.events || {}, b = o.billing || {};
  const n = o.users || 0;
  swap(body,
    h('div', { class: 'kpis' },
      kpi(String(n), 'Foydalanuvchilar'), kpi(String(st.active || 0), 'Kotib faol'),
      kpi(String(o.new_7d || 0), "Yangi (7 kun)"), kpi(String(o.seen_7d || 0), 'Ilovani ochgan (7 kun)')),
    b.enabled ? h('div', { class: 'kpis' },
      kpi(String(b.paid_active || 0), "To'lagan (faol)"), kpi((b.stars_total || 0) + ' ⭐', 'Jami tushum'),
      kpi(String(b.referrals || 0), 'Taklif orqali kelgan'), kpi(String(b.ref_paid || 0), "Ulardan to'lagan")) : null,
    h('div', { class: 'card' }, h('h3', {}, 'Xavfsizlik darajasi'),
      h('p', { class: 'muted small' }, "Sozlamalarga qarab: avtomatik rejim, notanishlarga, botlarga javob, havolalar."),
      barList([['🟢 Xavfsiz', sf.safe || 0, 'ok'], ["🟡 O'rta xavf", sf.medium || 0, 'warn'], ['🔴 Yuqori xavf', sf.high || 0, 'bad', 'risky']], n)),
    h('div', { class: 'card' }, h('h3', {}, 'Ish rejimi'),
      barList([['✍️ Qoralama', md.draft || 0], ['🤖 Avtomatik', md.auto || 0, 'warn', 'auto'], ['👀 Soya', md.shadow || 0], ["⏸ O'chiq", md.off || 0]], n)),
    h('div', { class: 'card' }, h('h3', {}, 'Foydalanish (oxirgi 7 kun)'),
      h('p', { class: 'muted small' }, "Hodisalar soniga qarab. Shubhali — Telegram cheklovi, ko'p rad etilgan javoblar yoki juda ko'p avtomatik xabar."),
      barList([['✅ Foydali', sg.ok || 0, 'ok'], ['💤 Nofaol', sg.idle || 0, '', 'idle'], ['👁 Kuzatish kerak', sg.watch || 0, 'warn', 'risky'], ['⚠️ Shubhali', sg.suspicious || 0, 'bad', 'risky']], n)),
    h('div', { class: 'card' }, h('h3', {}, 'Holat'),
      barList([['Faol', st.active || 0, 'ok', 'active'], ["To'xtatilgan", st.paused || 0, 'warn'], ['Ulanmagan', st.pending || 0, '', 'pending'],
        ['Uzilgan', st.revoked || 0], ["Xavfsizlik uchun to'xtatilgan", st.broken || 0, 'bad'], ['⛔ Bloklangan', o.banned || 0, 'bad', 'banned']], n)),
    h('div', { class: 'card' }, h('h3', {}, 'Hodisalar (7 kun, hammasi)'),
      h('ul', { class: 'top-list' },
        [['draft_created', '✍️ Qoralama tayyorlandi'], ['auto_sent', '🤖 Avtomatik yuborildi'], ['draft_sent', '✅ Qoralamadan yuborildi'],
          ['draft_rejected', '✖️ Rad etildi'], ['blocked', '🛡 Xavfli kiruvchi xabar'], ['output_rejected', '🚫 Filtr rad etgan javob'],
          ['llm_error', '⚠️ AI xatosi'], ['flood_wait', '🐢 Telegram cheklovi'], ['circuit_breaker', "🧯 Favqulodda to'xtash"]]
          .map(([k, label]) => h('li', {}, h('span', {}, label), h('b', {}, String(ev[k] || 0)))))));
  applyWidths(body);
}

async function adminUsers(body) {
  const filter = state.adminFilter || 'all';
  const q = h('input', { type: 'search', placeholder: 'Ism, @username yoki ID', value: state.adminQ || '' });
  let timer;
  q.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(() => { state.adminQ = q.value; load(); }, 350); });
  const chips = h('div', { class: 'chips' }, ADMIN_FILTERS.map(([k, label]) => h('button', { class: 'chip-btn' + (k === filter ? ' on' : ''),
    onclick: () => { state.adminFilter = k; haptic('tap'); renderMain(); } }, label)));
  const list = h('div', {}, h('div', { class: 'skeleton' }));
  swap(body, q, chips, list);
  const load = async () => {
    const p = new URLSearchParams({ filter, q: state.adminQ || '' });
    let r;
    try { r = await api('GET', '/admin/users?' + p); } catch (e) { swap(list, h('p', { class: 'err' }, e.message)); return; }
    swap(list, h('p', { class: 'muted small' }, `Topildi: ${r.total}` + (r.total > r.users.length ? ` (birinchi ${r.users.length} tasi)` : '')),
      r.users.length ? r.users.map(userRow) : h('div', { class: 'card soft' }, h('p', { class: 'muted' }, "Hech kim topilmadi.")));
  };
  await load();
}

function userRow(u) {
  const b = u.billing || {};
  return h('button', { class: 'card adm-user', onclick: () => openAdmin((v) => viewAdminUser(v, u.id)) },
    h('div', { class: 'row-between' },
      h('div', { class: 'adm-name' }, h('b', {}, userTitle(u)), u.admin ? ' 👑' : null,
        h('div', { class: 'muted small' }, [u.username ? '@' + u.username : null, 'ID ' + u.id].filter(Boolean).join(' · '))),
      u.banned ? badge({ x: ['Bloklangan', 'bad'] }, 'x') : (() => { const [l, c] = STATUS[u.status] || [u.status, '']; return h('span', { class: 'badge ' + c }, l); })()),
    h('div', { class: 'chips' },
      h('span', { class: 'badge' }, MODE_LABELS[u.mode] || u.mode), badge(SAFETY, u.safety), badge(SIGNAL, u.signal),
      b.enabled && !u.banned ? h('span', { class: 'badge accent' }, adminTierLabel(b)) : null),
    h('div', { class: 'muted small' }, `7 kun: ${u.prepared} tayyorlangan · ${u.sent} yuborilgan` + (u.approval >= 0 ? ` · tasdiq ${u.approval}%` : '') +
      ` · oxirgi faollik: ${u.last_event ? fmtAgo(u.last_event) : '—'}`));
}

function adminTierLabel(b) {
  if (b.exempt) return '👑 Admin';
  if (b.paid) return (TIER_NAMES[b.tier] || 'Obuna') + " · to'langan";
  if (b.bonus) return "🎁 Sovg'a/bonus" + (b.tier === 'pro' ? ' · Pro' : '');
  if (b.on_trial) return `Sinov · ${b.days_left} kun`;
  return 'Obunasiz';
}

async function viewAdminUser(view, id) {
  const head = h('div', { class: 'card-head' }, h('h1', {}, 'Foydalanuvchi'), h('button', { class: 'btn ghost sm', onclick: () => openAdmin() }, '← Orqaga'));
  put(view, head, h('div', { class: 'skeleton' }));
  let d;
  try { d = await api('GET', '/admin/users/' + id); } catch (e) { swap(view, head, h('p', { class: 'err' }, e.message)); return; }
  const u = d.user, b = u.billing || {}, s = d.settings || {}, c = d.counts || {}, c30 = d.counts30 || {};
  const reload = () => openAdmin((v) => viewAdminUser(v, id));
  const info = (label, value) => h('li', {}, h('span', {}, label), h('b', {}, value));
  swap(view, head,
    h('div', { class: 'card' },
      h('div', { class: 'card-head' }, h('h3', {}, userTitle(u) + (u.admin ? ' 👑' : '')),
        u.banned ? h('span', { class: 'badge bad' }, 'Bloklangan') : (() => { const [l, cl] = STATUS[u.status] || [u.status, '']; return h('span', { class: 'badge ' + cl }, l); })()),
      h('ul', { class: 'top-list' },
        info('Telegram ID', String(u.id)), u.username ? info('Username', '@' + u.username) : null,
        u.owner_name ? info('Sozlamadagi ism', u.owner_name) : null,
        info("Ro'yxatdan o'tgan", fmtDate(u.created_at)), info('Ilovani oxirgi ochgan', u.seen_at ? fmtAgo(u.seen_at) : '—'),
        info('Oxirgi faollik', u.last_event ? fmtAgo(u.last_event) : '—'), info('Rozilik', u.consented ? 'berilgan' : "yo'q"),
        u.note ? info('Holat izohi', u.note) : null,
        info('Obuna', b.enabled ? adminTierLabel(b) + (b.until ? ` · ${fmtDate(b.until)} gacha` : '') : "to'lov o'chiq"),
        info('Google', d.google ? 'ulangan' : "yo'q"), info("Qo'shimcha akkauntlar", String(d.linked || 0)), info('Vazifalar', String(d.schedules || 0)))),
    d.ban ? h('div', { class: 'card danger' }, h('h3', {}, '⛔ Bloklangan'),
      h('p', { class: 'small' }, (d.ban.reason ? 'Sabab: ' + d.ban.reason + ' · ' : '') + fmtDate(d.ban.at) + ` · admin ${d.ban.by}`)) : null,
    h('div', { class: 'card' },
      h('div', { class: 'card-head' }, h('h3', {}, 'Xavfsizlik'), badge(SAFETY, u.safety)),
      h('p', { class: 'small' }, 'Rejim: ', h('b', {}, MODE_LABELS[u.mode] || u.mode)),
      u.safety_notes.length ? h('ul', { class: 'note-list' }, u.safety_notes.map((x) => h('li', {}, x))) : h('p', { class: 'muted small' }, "Xavfli sozlama yo'q: javoblar faqat kontaktlarga, qoralama sifatida."),
      h('p', { class: 'muted small' }, [`Til: ${s.language}`, `uslub: ${PERSONA_LABELS[s.persona] || s.persona}`, `qoidalar: ${s.rules}`, s.quiet ? 'tinch soatlar yoqiq' : 'tinch soatlar o\'chiq',
        s.voice ? 'ovozga javob' : null, s.photos ? 'rasmga javob' : null, s.learn ? "o'qitish rejimi" : null, s.about ? "«O'zim haqimda» bor" : null,
        s.routine ? `kun tartibi: ${s.routine}` : null, s.knowledge ? 'bilim bazasi bor' : null].filter(Boolean).join(' · '))),
    h('div', { class: 'card' },
      h('div', { class: 'card-head' }, h('h3', {}, 'Foydalanish'), badge(SIGNAL, u.signal)),
      u.signal_notes.length ? h('ul', { class: 'note-list' }, u.signal_notes.map((x) => h('li', {}, x))) : null,
      h('table', { class: 'stats-table' },
        h('thead', {}, h('tr', {}, h('th', {}, 'Hodisa'), h('th', {}, '7 kun'), h('th', {}, '30 kun'))),
        h('tbody', {}, [['draft_created', 'Qoralama'], ['auto_sent', 'Avtomatik yuborilgan'], ['draft_sent', 'Qoralamadan yuborilgan'], ['draft_rejected', 'Rad etilgan'],
          ['skip', "O'tkazib yuborilgan"], ['blocked', 'Xavfli kiruvchi'], ['output_rejected', 'Filtr rad etgan'], ['llm_error', 'AI xatosi'],
          ['flood_wait', 'Telegram cheklovi'], ['circuit_breaker', "Favqulodda to'xtash"], ['fatal', 'Sessiya xatosi']]
          .map(([k, label]) => h('tr', {}, h('td', {}, label), h('td', {}, String(c[k] || 0)), h('td', {}, String(c30[k] || 0))))))),
    adminActions(u, b, reload),
    h('h2', {}, 'Oxirgi hodisalar'),
    h('div', { class: 'card' }, d.events.length
      ? h('ul', { class: 'timeline' }, d.events.slice(0, 25).map((e) => h('li', {}, h('i', { class: 'dot' }), h('div', {},
        h('div', {}, EVENTS[e.event] || e.event, e.detail ? h('span', { class: 'muted small' }, ' · ' + e.detail) : null),
        h('div', { class: 'when' }, fmtAgo(e.at))))))
      : h('p', { class: 'muted' }, "Hodisa yo'q.")),
    h('p', { class: 'muted small center' }, "Xabar matnlari ko'rsatilmaydi — faqat hodisalar va sozlamalar (maxfiylik)."));
}

function adminActions(u, b, reload) {
  const card = h('div', { class: 'card' }, h('h3', {}, 'Amallar'));
  // Sovg'a obuna
  if (b.enabled && !u.admin) {
    let days = 30, tier = 'pro', notify = true;
    const daySel = h('div', { class: 'chips' }, GRANT_DAYS.map(([d, label]) => h('button', { class: 'chip-btn' + (d === days ? ' on' : ''),
      onclick: (e) => { days = d; daySel.querySelectorAll('.chip-btn').forEach((x) => x.classList.remove('on')); e.currentTarget.classList.add('on'); haptic('tap'); } }, label)));
    const tierSel = h('div', { class: 'segmented' }, [['advanced', '⭐ Advanced'], ['pro', '🚀 Pro']].map(([k, label]) => h('button', { class: k === tier ? 'on' : '',
      onclick: (e) => { tier = k; tierSel.querySelectorAll('button').forEach((x) => x.classList.remove('on')); e.currentTarget.classList.add('on'); haptic('tap'); } }, label)));
    put(card,
      h('h4', {}, "🎁 Tekin (sovg'a) obuna"),
      h('p', { class: 'muted small' }, "Sovg'a joriy sinov yoki to'langan muddatdan keyin qo'shiladi; Telegram pul yechmaydi."),
      field('Muddat', daySel), field('Tarif', tierSel),
      switchRow('Foydalanuvchiga xabar berish', null, notify, (v) => { notify = v; }, true),
      h('button', { class: 'btn block', onclick: busy(async () => {
        const label = (GRANT_DAYS.find(([d]) => d === days) || [0, days + ' kun'])[1];
        if (!(await confirmBox(`${userTitle(u)} ga ${label} ${tier === 'pro' ? 'Pro' : 'Advanced'} obuna sovg'a qilinsinmi?`))) return;
        await api('POST', `/admin/users/${u.id}/grant`, { days, tier, notify });
        toast("Sovg'a berildi"); reload();
      }) }, "Sovg'a qilish"),
      b.bonus ? h('button', { class: 'btn ghost block', onclick: busy(async () => {
        if (!(await confirmBox("Sovg'a/bonus kunlari darhol tugatilsinmi? To'langan muddat va sinov o'zgarmaydi."))) return;
        await api('POST', `/admin/users/${u.id}/revoke-grant`);
        toast("Sovg'a bekor qilindi"); reload();
      }) }, "Sovg'ani bekor qilish") : null);
  }
  // Xabar
  const msg = h('textarea', { maxlength: 2000, rows: 3, placeholder: 'Foydalanuvchiga bot orqali xabar' });
  put(card, h('h4', {}, '📩 Xabar yuborish'), msg,
    h('button', { class: 'btn ghost block', onclick: busy(async () => {
      if (!msg.value.trim()) throw new Error('Matn kiriting');
      await api('POST', `/admin/users/${u.id}/message`, { text: msg.value });
      msg.value = ''; toast('Yuborildi');
    }) }, 'Yuborish'));
  if (u.admin) return card;
  // To'xtatish va bloklash
  if (u.status === 'active' && !u.banned) {
    put(card, h('h4', {}, "⏸ Vaqtincha to'xtatish"),
      h('p', { class: 'muted small' }, "Kotib to'xtaydi, foydalanuvchiga xabar boradi; u o'zi qayta yoqa oladi."),
      h('button', { class: 'btn ghost block', onclick: busy(async () => {
        if (!(await confirmBox("Kotib to'xtatilsinmi?"))) return;
        await api('POST', `/admin/users/${u.id}/pause`); toast("To'xtatildi"); reload();
      }) }, "To'xtatish"));
  }
  if (u.banned) {
    put(card, h('h4', {}, '⛔ Blok'), h('button', { class: 'btn block', onclick: busy(async () => {
      if (!(await confirmBox('Blok olib tashlansinmi? Foydalanuvchi kotibni qayta yoqa oladi.'))) return;
      await api('DELETE', `/admin/users/${u.id}/ban`); toast('Blok olib tashlandi'); reload();
    }) }, 'Blokni olib tashlash'));
  } else {
    const reason = h('input', { type: 'text', maxlength: 200, placeholder: 'Sabab (masalan: spam, firibgarlik)' });
    let notify = true;
    put(card, h('h4', {}, '⛔ Bloklash'),
      h('p', { class: 'muted small' }, "Kotib darhol to'xtaydi, ilova va bot yopiladi, avtomatik to'lov bekor qilinadi. Akkauntni o'chirib qayta ochish blokni olib tashlamaydi."),
      reason, switchRow('Foydalanuvchiga xabar berish', null, notify, (v) => { notify = v; }, true),
      h('button', { class: 'btn danger block', onclick: busy(async () => {
        if (!(await confirmBox(`${userTitle(u)} bloklansinmi?`))) return;
        await api('POST', `/admin/users/${u.id}/ban`, { reason: reason.value, notify });
        toast('Bloklandi'); reload();
      }) }, 'Bloklash'));
  }
  return card;
}

async function adminPayments(body) {
  const r = await api('GET', '/admin/payments');
  const ps = r.payments || [];
  if (!ps.length) { swap(body, h('div', { class: 'card soft' }, h('p', { class: 'muted' }, "Hozircha to'lov yo'q."))); return; }
  swap(body, h('p', { class: 'muted small' }, `Oxirgi ${ps.length} ta to'lov`),
    ps.map((p) => h('div', { class: 'card' },
      h('div', { class: 'row-between' }, h('b', {}, `${p.amount} ⭐`),
        p.refunded ? h('span', { class: 'badge warn' }, 'Qaytarilgan') : h('span', { class: 'badge ok' }, p.donation ? '💚 Hayriya' : p.recurring ? 'Obuna' : "To'lov")),
      h('div', { class: 'muted small' }, `${fmtDate(p.created_at)} · `, h('a', { href: '#', onclick: (e) => { e.preventDefault(); openAdmin((v) => viewAdminUser(v, p.owner_id)); } }, 'ID ' + p.owner_id)),
      h('div', { class: 'muted small break' }, p.charge_id),
      p.refunded ? null : h('button', { class: 'btn ghost sm', onclick: busy(async () => {
        if (!(await confirmBox(p.donation ? `${p.amount} ⭐ xayriya qaytarilsinmi? Hayriya hisobidan ayiriladi.` : `${p.amount} ⭐ foydalanuvchiga qaytarilsinmi? Shu to'lov bergan muddat darhol tugaydi.`))) return;
        await api('POST', '/admin/refund', { charge_id: p.charge_id });
        toast('Qaytarildi'); renderMain();
      }) }, 'Qaytarish'))));
}

async function adminBroadcast(body) {
  const ta = h('textarea', { maxlength: 2000, rows: 6, placeholder: 'Masalan: Kotibda yangi funksiya — «Kun tartibim»! Sozlamalarda sinab ko\'ring.' });
  const counter = h('div', { class: 'counter' });
  const upd = () => { counter.textContent = ta.value.length + ' / 2000'; };
  ta.addEventListener('input', upd); upd();
  swap(body, h('div', { class: 'card' },
    h('h3', {}, '📢 Hammaga xabar'),
    h('p', { class: 'muted small' }, "Rozilik bergan va bloklanmagan barcha foydalanuvchilarga bot orqali yuboriladi. Spam bo'lmasligi uchun kamdan-kam va faqat muhim xabarlar uchun ishlating."),
    ta, counter,
    h('button', { class: 'btn block', onclick: busy(async () => {
      if (!ta.value.trim()) throw new Error('Matn kiriting');
      if (!(await confirmBox('Xabar barcha foydalanuvchilarga yuborilsinmi? Buni qaytarib bo\'lmaydi.'))) return;
      const r = await api('POST', '/admin/broadcast', { text: ta.value });
      ta.value = ''; upd(); toast(`Yuborilmoqda: ${r.recipients} ta foydalanuvchi`);
    }) }, 'Yuborish')));
}

// ---------- Hayriya ----------
const DONATION_PRESETS = [50, 100, 500];

function openExternal(url) {
  if (tg && tg.openLink) tg.openLink(url); else window.open(url, '_blank', 'noopener');
}

// proofNode — isbot: https havola bo'lsa — bosiladigan, aks holda matn (tx hash).
function proofNode(proof) {
  if (!proof) return null;
  if (/^https:\/\/\S+$/.test(proof)) {
    return h('a', { href: proof, class: 'small break', onclick: (e) => { e.preventDefault(); openExternal(proof); } }, '🔗 Isbot');
  }
  return h('div', { class: 'muted small break' }, 'Isbot: ' + proof);
}

function payoutTitle(p) {
  if (p.kind === 'manual') return p.note ? `Qo'shimcha xayriya · ${p.note}` : "Qo'shimcha xayriya";
  const [y, m] = p.period.split('-');
  return `${MONTHS[+m - 1] || p.period} ${y}`;
}

function payoutRow(p, extra) {
  return h('li', {},
    h('div', { class: 'row-between' }, h('span', {}, payoutTitle(p)), h('b', {}, `${p.stars} ⭐`)),
    p.kind === 'monthly' ? h('div', { class: 'muted small' }, `Obunalardan ${p.share} ⭐ · xayriyalar ${p.donated} ⭐` + (p.adjust ? ` · tuzatish ${p.adjust > 0 ? '+' : ''}${p.adjust} ⭐` : '')) : null,
    h('div', { class: 'row' }, p.paid ? h('span', { class: 'badge ok' }, "O'tkazildi" + (p.paid_at ? ' · ' + fmtDate(p.paid_at) : '')) : h('span', { class: 'badge warn' }, 'Kutilmoqda'), proofNode(p.proof)),
    extra || null);
}

async function donate(stars) {
  const n = Math.floor(Number(stars));
  if (!(n >= 1 && n <= 10000)) throw new Error("Summa 1–10000 ⭐ bo'lishi kerak");
  const { link } = await api('POST', '/charity/donate', { stars: n });
  if (!tg || !tg.openInvoice) { if (tg && tg.openTelegramLink) tg.openTelegramLink(link); return; }
  tg.openInvoice(link, (status) => {
    if (status === 'paid') { haptic('success'); toast(`💚 Rahmat! ${n} ⭐ hayriyaga qo'shildi`); setTimeout(renderMain, 1500); }
    else if (status === 'failed') toast("To'lov amalga oshmadi", true);
  });
}

function charityCard() {
  const card = h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, '💚 Hayriya')), h('div', { class: 'skeleton' }));
  api('GET', '/charity').then((r) => {
    if (!r.enabled) { card.remove(); return; }
    const stat = (n, label) => h('div', { class: 'stat' }, h('div', { class: 'num' }, String(n)), h('div', { class: 'lbl' }, label));
    const custom = h('input', { type: 'number', min: 1, max: r.max_donation || 10000, inputmode: 'numeric', placeholder: 'Boshqa summa', class: 'grow' });
    swap(card,
      h('div', { class: 'card-head' }, h('h3', {}, '💚 Hayriya'), h('span', { class: 'badge ok' }, `${r.percent}%`)),
      h('p', { class: 'small' }, `Har obuna to'lovining ${r.percent}% i hayriyaga ajratiladi` + (r.name ? ` — «${r.name}»` : '') +
        ". Summa oyda bir marta tashkilotga o'tkaziladi va isboti shu yerda ko'rsatiladi."),
      r.url ? h('a', { href: r.url, class: 'small', onclick: (e) => { e.preventDefault(); openExternal(r.url); } }, 'Tashkilot haqida →') : null,
      h('div', { class: 'stats' }, stat(r.paid_stars + ' ⭐', "O'tkazilgan"), stat(r.accruing_stars + ' ⭐', "Shu oy yig'ilmoqda")),
      r.mine ? h('p', { class: 'small' }, `💚 Sizning hissangiz: ${r.mine} ⭐ — har oy o'tkazilgach, bot sizga isboti bilan xabar beradi.`) : null,
      r.pending_stars ? h('p', { class: 'muted small' }, `O'tkazilishi kutilmoqda: ${r.pending_stars} ⭐ (Stars 21 kundan keyin yechib olinadi)`) : null,
      h('h3', {}, "O'zingiz ham qo'shing"),
      h('p', { class: 'muted small' }, "Stars balansingizdan — to'liq hayriyaga ketadi."),
      h('div', { class: 'chips' }, DONATION_PRESETS.map((n) => h('button', { class: 'chip-btn', onclick: busy(() => donate(n)) }, `💚 ${n} ⭐`))),
      h('div', { class: 'row nowrap' }, custom, h('button', { class: 'btn sm', onclick: busy(() => donate(custom.value)) }, "Qo'shish")),
      r.payouts.length ? [h('h3', {}, 'Hisobot'), h('ul', { class: 'payouts' }, r.payouts.map((p) => payoutRow(p)))] : null);
  }).catch(() => card.remove());
  return card;
}

async function adminCharity(body) {
  const r = await api('GET', '/charity');
  if (!r.enabled) {
    swap(body, h('div', { class: 'card soft' }, h('p', { class: 'muted' }, "Hayriya yoqilmagan. Serverda CHARITY_PERCENT (masalan 10) ni bering.")));
    return;
  }
  const stars = h('input', { type: 'number', min: 1, inputmode: 'numeric', placeholder: 'Summa (⭐ ekvivalenti)' });
  const note = h('input', { type: 'text', maxlength: 200, placeholder: 'Izoh: masalan, «Mehr» fondiga naqd' });
  const proof = h('input', { type: 'text', maxlength: 300, placeholder: 'Isbot: tx hash yoki havola (ixtiyoriy)' });
  const pending = r.payouts.filter((p) => !p.paid && p.stars > 0);
  swap(body,
    h('div', { class: 'kpis' }, kpi(r.paid_stars + ' ⭐', "O'tkazilgan"), kpi(r.pending_stars + ' ⭐', 'Kutilmoqda'),
      kpi(r.accruing_stars + ' ⭐', "Shu oy yig'ilmoqda"), kpi(r.percent + '%', 'Ulush')),
    h('div', { class: 'card soft' }, h('p', { class: 'small' },
      "Oy tugagach summa avtomatik hisoblanadi va sizga bot orqali xabar keladi. Stars'ni @BotFather → Balance (Fragment, 21 kunlik ushlab turishdan keyin) yechib, " +
      "tashkilotga o'tkazing va quyida isbotini kiriting.")),
    h('div', { class: 'card' }, h('h3', {}, "O'tkazilishi kerak"),
      pending.length ? h('ul', { class: 'payouts' }, pending.map((p) => {
        const inp = h('input', { type: 'text', maxlength: 300, placeholder: 'tx hash yoki https://… havola', class: 'grow' });
        return payoutRow(p, h('div', { class: 'row nowrap' }, inp, h('button', { class: 'btn sm', onclick: busy(async () => {
          if (!inp.value.trim()) throw new Error('Isbot kiriting');
          await api('POST', `/admin/charity/${p.id}/paid`, { proof: inp.value.trim() });
          toast("O'tkazildi deb belgilandi"); renderMain();
        }) }, "O'tkazildi")));
      })) : h('p', { class: 'muted small' }, "Hammasi o'tkazilgan ✅")),
    h('div', { class: 'card' }, h('h3', {}, "💚 O'zim qo'shaman"),
      h('p', { class: 'muted small' }, "Stars bilan: balansingizdan botga o'tadi va keyingi oylik summaga qo'shiladi. Stars sotib olish ularni yechib olishdan qimmat, " +
        "shuning uchun katta summani to'g'ridan-to'g'ri (pul/TON) o'tkazib, pastda yozib qo'ygan ma'qul."),
      h('div', { class: 'chips' }, DONATION_PRESETS.map((n) => h('button', { class: 'chip-btn', onclick: busy(() => donate(n)) }, `⭐ ${n}`))),
      h('h3', {}, "Stars'siz xayriyani yozish"),
      field('Summa', stars), field('Izoh', note), field('Isbot', proof),
      h('button', { class: 'btn block', onclick: busy(async () => {
        await api('POST', '/admin/charity/manual', { stars: Math.floor(Number(stars.value)), note: note.value.trim(), proof: proof.value.trim() });
        toast('Yozildi — rahmat! 💚'); renderMain();
      }) }, 'Yozish')),
    r.payouts.length ? h('div', { class: 'card' }, h('h3', {}, 'Tarix'), h('ul', { class: 'payouts' }, r.payouts.map((p) => payoutRow(p)))) : null);
}

// ---------- Do'stni taklif qilish ----------
function shareReferral(info) {
  const text = `Telegram'dagi xabarlarimga AI kotib javob tayyorlab beradi — ${info.trial_days} kun bepul sinab ko'ring:`;
  const url = 'https://t.me/share/url?url=' + encodeURIComponent(info.link) + '&text=' + encodeURIComponent(text);
  haptic('tap');
  if (tg && tg.openTelegramLink) tg.openTelegramLink(url); else window.open(url, '_blank', 'noopener');
}

function referralCard() {
  const card = h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, "🎁 Do'stni taklif qilish")), h('div', { class: 'skeleton' }));
  api('GET', '/referral').then((r) => {
    if (!r.enabled) { card.remove(); return; }
    const stat = (n, label) => h('div', { class: 'stat' }, h('div', { class: 'num' }, String(n)), h('div', { class: 'lbl' }, label));
    swap(card,
      h('div', { class: 'card-head' }, h('h3', {}, "🎁 Do'stni taklif qilish"),
        r.earned_days ? h('span', { class: 'badge ok' }, `+${r.earned_days} kun olindi`) : null),
      h('ul', { class: 'perks' },
        h('li', {}, `Do'stingiz ${r.trial_days} kun bepul sinaydi (odatdagidan ${r.trial_bonus} kun ko'p)`),
        h('li', {}, `U Telegram akkauntini ulasa — sizga +${r.activated_days} kun`),
        h('li', {}, `U obuna bo'lsa — sizga +${r.paid_days} kun (bir oy bepul)`)),
      h('div', { class: 'stats' }, stat(r.invited, 'Taklif qilingan'), stat(r.paid, "Obuna bo'lgan")),
      r.link ? [
        h('div', { class: 'hook-list' },
          h('button', { class: 'btn block', onclick: () => shareReferral(r) }, "Do'stga yuborish"),
          h('button', { class: 'btn ghost block', onclick: () => copyText(r.link) }, 'Havoladan nusxa olish')),
        h('p', { class: 'muted small center break' }, r.link),
      ] : h('p', { class: 'muted small' }, "Havola hozircha mavjud emas — botda /taklif buyrug'ini yuboring."),
      h('p', { class: 'muted small' }, `Bonus kunlarda kotib cheklovsiz ishlaydi. Ulangan do'stlar uchun bonus ko'pi bilan ${r.activated_cap} marta, obuna bo'lganlar uchun — cheksiz.`));
  }).catch(() => card.remove());
  return card;
}

async function pay(tier) {
  const { link } = await api('POST', '/billing/invoice', { tier: tier || 'advanced' });
  if (!tg || !tg.openInvoice) { if (tg && tg.openTelegramLink) tg.openTelegramLink(link); return; }
  tg.openInvoice(link, async (status) => {
    if (status === 'paid') {
      toast("To'lov qabul qilindi — obuna faollashmoqda…");
      // Obuna server tomonda Telegram tasdig'i (successful_payment) kelgach yoqiladi.
      for (let i = 0; i < 10; i++) {
        await new Promise((r) => setTimeout(r, 1500));
        try { await refreshMe(); } catch (_) { /* */ }
        if (state.me.billing && state.me.billing.paid && (!tier || state.me.billing.tier === tier)) break;
      }
      renderMain();
    } else if (status === 'failed') {
      toast("To'lov amalga oshmadi", true);
    }
  });
}

boot();
