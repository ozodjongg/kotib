'use strict';
// Kotib Mini App. Xavfsizlik: barcha dinamik matn textContent orqali qo'yiladi
// (innerHTML ishlatilmaydi) — kontakt xabarlari XSS bera olmaydi. Inline skript/stil yo'q (qat'iy CSP).

const tg = window.Telegram && window.Telegram.WebApp;
const root = document.getElementById('app');
const nav = document.getElementById('nav');
const toastEl = document.getElementById('toast');

const PERSONA_LABELS = {
  neutral: "Do'stona (standart)", joke: 'Hazil ruhida', son: "O'g'il nomidan",
  formal: 'Rasmiy', brief: 'Juda qisqa', custom: "O'z ko'rsatmam",
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
  referral_bonus: "Do'st taklifi uchun bonus kunlar", away_set: "Holat o'zgartirildi",
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
  const res = await fetch('/api' + path, {
    method,
    headers: { 'Authorization': 'tma ' + (tg ? tg.initData : ''), 'Content-Type': 'application/json' },
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
    try { if (tg.BackButton) tg.BackButton.onClick(() => go('home')); } catch (_) { /* */ }
    bioInit();
  }
  if (!tg || !tg.initData) {
    swap(root, h('div', { class: 'empty' },
      h('img', { class: 'logo', src: 'logo.svg', alt: 'Kotib', width: 56, height: 56 }),
      h('div', { class: 'big' }, 'Ilovani Telegram ichidan oching'),
      h('p', { class: 'muted' }, 'Kotib faqat Telegram bot orqali ishlaydi: botga /start yozing va «Ochish» tugmasini bosing.')));
    return;
  }
  try {
    await refreshMe();
    state.settings = await api('GET', '/settings');
    if (!Array.isArray(state.settings.rules)) state.settings.rules = [];
    state.savedMode = state.settings.mode;
    if (!state.settings.quiet_hours) state.settings.quiet_hours = { enabled: false, start: '23:00', end: '07:00', tz: 'Asia/Tashkent' };
  } catch (e) {
    swap(root, h('div', { class: 'empty' },
      h('div', { class: 'big' }, 'Ochilmadi'),
      h('p', { class: 'err' }, e.message),
      h('button', { class: 'btn', onclick: () => location.reload() }, 'Qayta urinish')));
    return;
  }
  if (state.me.consented && needsConnect()) await resumeLogin();
  render();
  setInterval(backgroundRefresh, 20000);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) backgroundRefresh(); });
}

async function refreshMe() { state.me = await api('GET', '/me'); }

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

// resumeLogin — Mini App qayta ochilganda serverda davom etayotgan ulanishni (QR/kod) tiklaydi.
async function resumeLogin() {
  try {
    const st = await api('GET', '/login/state');
    if (st && ['wait_qr', 'wait_code', 'wait_password', 'connecting'].includes(st.step)) state.login = st;
  } catch (_) { /* yo'q bo'lsa — oddiy shakl */ }
}

function render() {
  if (!state.me.consented) return renderConsent();
  if (needsConnect()) return renderConnect();
  renderMain();
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
      h('label', { class: 'row' }, agree, h('span', { class: 'small' }, 'Tushundim va roziman'))),
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
  let body;
  if (st.step === 'wait_code') {
    const code = h('input', { type: 'tel', inputmode: 'numeric', autocomplete: 'one-time-code', placeholder: '12345', maxlength: 8 });
    body = [h('h3', {}, 'Kodni kiriting'),
      h('p', { class: 'muted' }, codeWhere(st)), code,
      h('div', { class: 'card accent' }, h('p', { class: 'small' }, "Kodni hech kimga yubormang va chatga yozmang — Telegram kirishni bloklashi mumkin.")),
      h('button', { class: 'btn block', onclick: busy(() => submitLogin('/login/code', { code: code.value })) }, 'Tasdiqlash')];
    setTimeout(() => code.focus(), 50);
  } else if (st.step === 'wait_password') {
    const pw = h('input', { type: 'password', placeholder: 'Ikki bosqichli parol', autocomplete: 'current-password' });
    body = [h('h3', {}, 'Ikki bosqichli parol'),
      h('p', { class: 'muted' }, 'Akkauntingizda 2FA yoqilgan. Parol saqlanmaydi — faqat bir marta ishlatiladi.'), pw,
      h('button', { class: 'btn block', onclick: busy(() => submitLogin('/login/password', { password: pw.value })) }, 'Tasdiqlash')];
  } else if (st.step === 'wait_qr') {
    // Mini App ichida tg:// havola ochilmaydi (Telegram openLink faqat https qabul qiladi): tashqi
    // brauzerda o'z sahifamizni ochamiz, u tg://login'ni Telegram ilovasiga uzatadi.
    const token = (st.qr_url || '').split('token=')[1] || '';
    const confirm = h('button', { class: 'btn block', onclick: () => {
      haptic('tap');
      const url = location.origin + '/tglogin?token=' + encodeURIComponent(token);
      if (tg && tg.openLink) tg.openLink(url); else location.href = st.qr_url;
    } }, "Telegram'da tasdiqlash");
    const secs = st.qr_expires ? Math.max(0, Math.round((new Date(st.qr_expires) - Date.now()) / 1000)) : null;
    body = [h('h3', {}, "Telegram'da tasdiqlang"),
      h('p', { class: 'muted' }, "Tugmani bosing — brauzer ochiladi va Telegram'ni chaqiradi (kerak bo'lsa «Telegram'ni ochish»ni bosing). Telegram kirishni tasdiqlashni so'raydi — tasdiqlang va shu yerga qayting. Kod kerak emas."),
      confirm,
      h('div', { class: 'card accent' }, h('p', { class: 'small' }, "Faqat shu so'rovni o'zingiz boshlagan bo'lsangiz tasdiqlang. Kotib faqat shu oynani ochgan akkauntingizga ulanadi — boshqa akkaunt tasdiqlasa, ulanish bekor qilinadi.")),
      h('details', {},
        h('summary', { class: 'small' }, "Tugma ishlamadimi? Boshqa qurilmadan QR"),
        h('ol', { class: 'small muted' },
          h('li', {}, "Kompyuter yoki boshqa telefondagi Telegram'ni oching (shu akkaunt bilan)."),
          h('li', {}, 'Sozlamalar → Qurilmalar → «Kompyuterni ulash» (Link Desktop Device).'),
          h('li', {}, 'Quyidagi QR kodni skanerlang.')),
        st.qr_image ? h('img', { class: 'qr', src: st.qr_image, alt: 'Kirish QR kodi', width: 220, height: 220 }) : null),
      secs != null ? h('p', { class: 'muted small center' }, `Kod ${secs} soniyadan keyin avtomatik yangilanadi.`) : null];
    setTimeout(pollQR, 2000);
  } else if (st.step === 'connecting') {
    body = [h('h3', {}, 'Ulanmoqda…'), h('div', { class: 'skeleton' })];
    setTimeout(pollLogin, 1500);
  } else if (st.step === 'done') {
    body = [h('h3', {}, 'Ulandi ✓'), h('p', { class: 'muted' }, 'Hammasi tayyor. Endi kotibni sozlang.'),
      h('button', { class: 'btn block', onclick: busy(async () => { await refreshMe(); state.tab = 'home'; render(); }) }, 'Davom etish')];
  } else {
    const phone = h('input', { type: 'tel', placeholder: '+998 90 123 45 67', autocomplete: 'tel' });
    const dead = state.me.status === 'revoked' && state.me.note;
    body = [h('h3', {}, dead ? 'Akkauntni qayta ulang' : 'Telegram akkauntini ulang'),
      dead ? h('div', { class: 'card accent' }, h('p', { class: 'small' }, state.me.note + '. Bu sessiya endi ishlamaydi — qayta kiring. Qoidalar va sozlamalaringiz saqlangan.')) : null,
      h('p', { class: 'muted' }, "Eng oson yo'l — kodsiz: Telegram ilovangizda bir marta «Tasdiqlash»ni bosasiz."),
      h('button', { class: 'btn block', onclick: busy(async () => {
        await api('POST', '/login/qr');
        state.login = { step: 'connecting' }; render();
      }) }, "Telegram orqali ulash (kodsiz)"),
      h('details', {},
        h('summary', { class: 'small' }, 'Yoki telefon raqam va kod bilan'),
        h('div', { class: 'stack' },
          h('p', { class: 'muted small' }, 'Kod faqat shu oynaga kiritiladi.'), phone,
          h('button', { class: 'btn ghost block', onclick: busy(async () => {
            await api('POST', '/login/phone', { phone: phone.value });
            state.login = { step: 'connecting' }; render();
          }) }, 'Kod olish')))];
  }
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

// pollQR — QR kutish holatida: token yangilansa yoki tasdiqlansa ekranni yangilaydi (miltillamasin).
async function pollQR() {
  if (state.login.step !== 'wait_qr') return;
  const prev = state.login.qr_url;
  try {
    const st = await api('GET', '/login/state');
    if (st.step === 'wait_qr' && st.qr_url === prev) { setTimeout(pollQR, 2000); return; }
    state.login = st;
    if (st.step === 'done') await refreshMe();
  } catch (_) { setTimeout(pollQR, 3000); return; }
  if (state.login.step === 'error') haptic('error');
  if (state.login.step === 'done') haptic('success');
  renderConnect();
}

async function pollLogin() {
  try {
    state.login = await api('GET', '/login/state');
    if (state.login.step === 'done') await refreshMe();
  } catch (_) { /* keyingi urinishda */ }
  if (!state.me.consented || !(needsConnect() || state.login.step === 'done')) return;
  if (state.login.step === 'error') haptic('error');
  renderConnect();
}

// ---------- asosiy oyna ----------
const TABS = [
  ['home', 'Asosiy', 'home'], ['drafts', 'Qoralamalar', 'inbox'], ['rules', 'Qoidalar', 'rules'],
  ['settings', 'Sozlamalar', 'settings'], ['account', 'Hisob', 'user'],
];

function go(tab) {
  if (state.tab === tab) return;
  state.tab = tab;
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
  try { if (tg && tg.BackButton) { if (state.tab === 'home') tg.BackButton.hide(); else tg.BackButton.show(); } } catch (_) { /* */ }
  const view = h('div', {});
  swap(root, view);
  ({ home: viewHome, drafts: viewDrafts, rules: viewRules, settings: viewSettings, account: viewAccount })[state.tab](view);
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
  ]],
  ['pro', '🚀 Pro', [
    "Advanced'dagi hammasi",
    '🎤 Ovozli xabarlarga javob',
    '📅 Google Calendar, Contacts, Tasks',
    '📱 Telefon avtomatikasi (Shortcuts / MacroDroid)',
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
    h('h2', {}, 'Maslahatlar'),
    h('div', { class: 'card soft' },
      h('ul', { class: 'perks' },
        h('li', {}, "«Sozlamalar»da ismingizni yozing — javoblar tabiiyroq chiqadi"),
        h('li', {}, "«Qoidalar»da oila yoki ishdagilar uchun alohida uslub bering"),
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
      switchRow('Botlarga ham', "Faqat qoralama: bot bilan cheksiz yozishma bo'lmasligi uchun avtomatik yuborilmaydi", s.reply_bots, (v) => { s.reply_bots = v; }),
      switchRow('Javobda havolalar', "O'chiq bo'lsa, havolali javoblar qoralamada qoladi", s.allow_links, (v) => { s.allow_links = v; }),
      switchRow('Uchrashuvlarni taqvimga taklif qilish', "Suhbatda aniq vaqtli uchrashuv chiqsa, bot «Taqvimga qo'shaymi?» deb so'raydi. Google ulanmagan bo'lsa ham ishlaydi (iPhone/Android taqvimi).", !s.no_suggest, (v) => { s.no_suggest = !v; }),
      switchRow('Kechki xulosa', "Har kuni soat 21:00 da: nechta javob tayyorlandi, kim javob kutyapti, qancha vaqt tejaldi.", !s.no_digest, (v) => { s.no_digest = !v; }),
      switchRow("«Keyinroq yozaman» eslatmasi", "Kotib kimgadir «keyinroq yozaman» desa va 3 soat ichida o'zingiz yozmasangiz — bot eslatadi.", !s.no_followups, (v) => { s.no_followups = !v; }),
      switchRow('Qoralamalar bot chatida', "Qoralama botga tugmalar bilan keladi: bir bosishda yuborasiz yoki xabarga javob qilib matnni o'zgartirasiz. Matnlar bot chatida saqlanib qoladi.", s.drafts_in_bot, (v) => { s.drafts_in_bot = v; })),
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
  put(view, h('h1', {}, 'Hisob'));
  const b = state.me.billing || {};
  if (b.enabled) put(view, subscriptionCard(b));
  if (b.enabled && !b.exempt) put(view, referralCard());
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
      h('h3', {}, "Akkauntni uzish va o'chirish"),
      h('p', { class: 'muted small' }, "Telegram sessiyasi tugatiladi, sozlamalar va qoralamalar o'chadi, obuna yangilanishi to'xtatiladi."),
      h('button', { class: 'btn danger block', onclick: busy(async () => {
        if (!(await confirmBox("Hamma narsa o'chiriladi. Bu amalni ortga qaytarib bo'lmaydi. Davom etasizmi?"))) return;
        await bioGate("Akkauntni o'chirish");
        await api('DELETE', '/account', { confirm: 'DELETE' });
        toast("O'chirildi");
        setTimeout(() => { if (tg) tg.close(); }, 800);
      }) }, "Uzish va o'chirish")));

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
    return h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, 'Obuna'), h('span', { class: 'badge ok' }, 'Admin')),
      h('p', { class: 'muted small' }, "Siz administratorsiz — kotib siz uchun bepul ishlaydi."));
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
      b.bonus ? h('p', { class: 'small' }, `🎁 Do'st taklifi uchun bonus: ${fmtDate(b.bonus_until)} gacha cheklovsiz ishlaydi. Keyin davom ettirish uchun obuna bo'ling.`) : null,
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
