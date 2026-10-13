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
  }
  if (!tg || !tg.initData) {
    swap(root, h('div', { class: 'empty' },
      h('div', { class: 'logo' }, 'K'),
      h('div', { class: 'big' }, 'Ilovani Telegram ichidan oching'),
      h('p', { class: 'muted' }, 'Kotib faqat Telegram bot orqali ishlaydi: botga /start yozing va «Ochish» tugmasini bosing.')));
    return;
  }
  try {
    await refreshMe();
    state.settings = await api('GET', '/settings');
    if (!Array.isArray(state.settings.rules)) state.settings.rules = [];
    if (!state.settings.quiet_hours) state.settings.quiet_hours = { enabled: false, start: '23:00', end: '07:00', tz: 'Asia/Tashkent' };
  } catch (e) {
    swap(root, h('div', { class: 'empty' },
      h('div', { class: 'big' }, 'Ochilmadi'),
      h('p', { class: 'err' }, e.message),
      h('button', { class: 'btn', onclick: () => location.reload() }, 'Qayta urinish')));
    return;
  }
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
    h('div', { class: 'logo' }, 'K'),
    h('div', { class: 'hero' },
      h('h1', {}, 'Kotibingiz bilan tanishing'),
      h('p', { class: 'muted' }, "Kiruvchi shaxsiy xabarlaringizga sizning uslubingizda javob qoralamasini tayyorlaydi. Siz tasdiqlamaguncha hech narsa yuborilmaydi.")),
    h('div', { class: 'card' },
      h('ul', { class: 'perks' },
        h('li', {}, 'Standart rejim — qoralama: har bir javobni o\'zingiz ko\'rib yuborasiz'),
        h('li', {}, 'Faqat kontaktlaringizga; guruh, kanal va botlarga javob yo\'q'),
        h('li', {}, 'Pul, parol, hujjat kabi sezgir mavzular hech qachon avtomatik yuborilmaydi'),
        h('li', {}, 'Sessiya shifrlangan; istalgan payt uzib, hammasini o\'chirishingiz mumkin')),
      b.enabled ? h('p', { class: 'muted' }, `Birinchi ${b.trial_days} kun bepul, keyin oyiga ${b.price} ⭐ (Telegram Stars).`) : null),
    h('div', { class: 'card soft' },
      h('p', { class: 'small muted' }, "Muhim: Telegram avtomatik javoblarni cheklashi mumkin — akkauntingiz cheklanish xavfi doim mavjud. Suhbatdoshlaringizning xabarlari javob tayyorlash uchun AI xizmatiga (Google Gemini) yuboriladi. Javoblar siz nomidan ketadi — mas'uliyat sizda."),
      h('label', { class: 'row' }, agree, h('span', { class: 'small' }, 'Tushundim va roziman'))),
    go);
}

// ---------- akkauntni ulash ----------
function renderConnect() {
  nav.hidden = true;
  const st = state.login;
  let body;
  if (st.step === 'wait_code') {
    const code = h('input', { type: 'tel', inputmode: 'numeric', autocomplete: 'one-time-code', placeholder: '12345', maxlength: 8 });
    body = [h('h3', {}, 'Kodni kiriting'),
      h('p', { class: 'muted' }, "Telegram ilovangizga kelgan kodni yozing."), code,
      h('div', { class: 'card accent' }, h('p', { class: 'small' }, "Kodni hech kimga yubormang va chatga yozmang — Telegram kirishni bloklashi mumkin.")),
      h('button', { class: 'btn block', onclick: busy(() => submitLogin('/login/code', { code: code.value })) }, 'Tasdiqlash')];
    setTimeout(() => code.focus(), 50);
  } else if (st.step === 'wait_password') {
    const pw = h('input', { type: 'password', placeholder: 'Ikki bosqichli parol', autocomplete: 'current-password' });
    body = [h('h3', {}, 'Ikki bosqichli parol'),
      h('p', { class: 'muted' }, 'Akkauntingizda 2FA yoqilgan. Parol saqlanmaydi — faqat bir marta ishlatiladi.'), pw,
      h('button', { class: 'btn block', onclick: busy(() => submitLogin('/login/password', { password: pw.value })) }, 'Tasdiqlash')];
  } else if (st.step === 'connecting') {
    body = [h('h3', {}, 'Ulanmoqda…'), h('div', { class: 'skeleton' })];
    setTimeout(pollLogin, 1500);
  } else if (st.step === 'done') {
    body = [h('h3', {}, 'Ulandi ✓'), h('p', { class: 'muted' }, 'Hammasi tayyor. Endi kotibni sozlang.'),
      h('button', { class: 'btn block', onclick: busy(async () => { await refreshMe(); state.tab = 'home'; render(); }) }, 'Davom etish')];
  } else {
    const phone = h('input', { type: 'tel', placeholder: '+998 90 123 45 67', autocomplete: 'tel' });
    body = [h('h3', {}, 'Telegram akkauntini ulang'),
      h('p', { class: 'muted' }, 'Kotib ishlashi uchun telefon raqamingiz bilan kiring. Kod faqat shu oynaga kiritiladi.'), phone,
      h('button', { class: 'btn block', onclick: busy(async () => {
        await api('POST', '/login/phone', { phone: phone.value });
        state.login = { step: 'connecting' }; render();
      }) }, 'Kod olish')];
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

function billingLine(b) {
  if (!b || !b.enabled || b.exempt) return null;
  if (b.paid) return b.canceled ? `Obuna ${fmtDate(b.paid_until)} gacha (yangilanmaydi)` : `Obuna faol · keyingi to'lov ${fmtDate(b.paid_until)}`;
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
      h('button', { class: 'btn', onclick: () => go('account') }, `Obunani yoqish · ${b.price} ⭐`)) : null,
    me.status === 'broken' ? h('div', { class: 'card danger' },
      h('h3', {}, "Xavfsizlik uchun to'xtatildi"),
      h('p', { class: 'small muted' }, (me.note ? me.note + '. ' : '') + "Telegram cheklov qo'ygan bo'lishi mumkin. Bir necha soat kutib, keyin davom ettiring.")) : null,
    h('div', { class: 'card' },
      h('div', { class: 'card-head' }, h('h3', {}, 'Holat'), statusBadge()),
      h('p', { class: 'muted small' }, `Rejim: ${modeName}` + (billingLine(b) ? ' · ' + billingLine(b) : '')),
      h('div', { class: 'actions' }, running
        ? h('button', { class: 'btn ghost', onclick: busy(async () => { await api('POST', '/pause'); await refreshMe(); toast("To'xtatildi"); renderMain(); }) }, "To'xtatish")
        : h('button', { class: 'btn', onclick: busy(async () => { await api('POST', '/resume'); await refreshMe(); toast('Davom ettirildi'); renderMain(); }) }, 'Davom ettirish'))),
    h('div', { class: 'stats' },
      h('button', { class: 'stat', onclick: () => go('drafts') }, h('div', { class: 'num' }, String(me.pending_drafts || 0)), h('div', { class: 'lbl' }, 'Kutilayotgan qoralama')),
      h('button', { class: 'stat', onclick: () => go('rules') }, h('div', { class: 'num' }, String(state.settings.rules.length)), h('div', { class: 'lbl' }, 'Faol qoidalar'))),
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
        await api('POST', `/drafts/${d.id}/send`, { reply: text.value });
        removeCard(); toast('Yuborildi');
      }) }, 'Yuborish')));
  return card;
}

// ---------- Sozlamalar ----------
function field(label, control, hint) {
  return h('div', { class: 'field' }, h('label', {}, label), control, hint ? h('div', { class: 'hint' }, hint) : null);
}
function switchRow(title, desc, checked, onchange) {
  const input = h('input', { type: 'checkbox', checked: !!checked, onchange: (e) => { haptic('tap'); onchange(e.target.checked); markDirty(); } });
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
    await api('PUT', '/settings', state.settings);
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
  const tzList = TIMEZONES.some(([k]) => k === s.quiet_hours.tz) ? TIMEZONES : [[s.quiet_hours.tz, s.quiet_hours.tz], ...TIMEZONES];

  put(view, 
    h('h1', {}, 'Sozlamalar'),
    h('div', { class: 'card' },
      field('Ish rejimi', modeControl(s), 'Qoralama — siz tasdiqlaysiz (tavsiya). Avto — darhol yuboriladi.'),
      field('Ismingiz', h('input', { type: 'text', value: s.owner_name || '', maxlength: 64, placeholder: 'Masalan: Aziz',
        oninput: (e) => { s.owner_name = e.target.value; markDirty(); } }), 'AI javoblarni sizning nomingizdan yozadi.'),
      field('Javob tili', select([['auto', 'Suhbatdosh tilida'], ['uz', "O'zbekcha"], ['ru', 'Ruscha'], ['en', 'Inglizcha']], s.language, (v) => { s.language = v; })),
      field('Uslub', personaSelect(s.default_persona, (v) => { s.default_persona = v; })),
      field("O'z ko'rsatmangiz", custom, "Uslub «O'z ko'rsatmam» bo'lsa ishlatiladi.")),
    h('h2', {}, 'Kimga javob beriladi'),
    h('div', { class: 'card' },
      switchRow("Kontaktda yo'qlarga ham", 'Tavsiya etilmaydi: spam va firibgarlar ko\'p', s.reply_unknown, (v) => { s.reply_unknown = v; }),
      switchRow('Botlarga ham', "Faqat qoralama: bot bilan cheksiz yozishma bo'lmasligi uchun avtomatik yuborilmaydi", s.reply_bots, (v) => { s.reply_bots = v; }),
      switchRow('Javobda havolalar', "O'chiq bo'lsa, havolali javoblar qoralamada qoladi", s.allow_links, (v) => { s.allow_links = v; })),
    h('h2', {}, 'Tinch soatlar'),
    h('div', { class: 'card' },
      switchRow('Tunda javob yozilmasin', 'Shu vaqt oralig\'ida kotib jim turadi', s.quiet_hours.enabled, (v) => { s.quiet_hours.enabled = v; }),
      h('div', { class: 'two' },
        field('Boshlanishi', h('input', { type: 'time', value: s.quiet_hours.start, onchange: (e) => { s.quiet_hours.start = e.target.value; markDirty(); } })),
        field('Tugashi', h('input', { type: 'time', value: s.quiet_hours.end, onchange: (e) => { s.quiet_hours.end = e.target.value; markDirty(); } }))),
      field('Vaqt mintaqasi', select(tzList, s.quiet_hours.tz, (v) => { s.quiet_hours.tz = v; }))),
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
async function viewAccount(view) {
  put(view, h('h1', {}, 'Hisob'));
  const b = state.me.billing || {};
  if (b.enabled) put(view, subscriptionCard(b));

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

function subscriptionCard(b) {
  if (b.exempt) {
    return h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, 'Obuna'), h('span', { class: 'badge ok' }, 'Admin')),
      h('p', { class: 'muted small' }, "Siz administratorsiz — kotib siz uchun bepul ishlaydi."));
  }
  const total = b.paid ? 30 : Math.max(1, b.trial_days || 1);
  const pct = Math.max(0, Math.min(100, (b.days_left / total) * 100));
  let status, body;
  if (b.paid) {
    status = h('span', { class: 'badge ' + (b.canceled ? 'warn' : 'ok') }, b.canceled ? 'Yangilanmaydi' : 'Faol');
    body = [
      h('p', { class: 'muted small' }, b.canceled ? `${fmtDate(b.paid_until)} gacha amal qiladi, keyin to'xtaydi.` : `Keyingi to'lov: ${fmtDate(b.paid_until)} · ${b.price} ⭐`),
      h('div', { class: 'progress' }, h('i', { style: null, 'data-w': pct })),
      b.renewable
        ? h('button', { class: 'btn block', onclick: busy(async () => { await api('POST', '/billing/renew'); await refreshMe(); toast('Obuna qayta yoqildi'); renderMain(); }) }, 'Obunani qayta yoqish')
        : h('button', { class: 'btn ghost block', onclick: busy(async () => {
          if (!(await confirmBox(`Obuna ${fmtDate(b.paid_until)} gacha ishlaydi, keyin yangilanmaydi. Bekor qilinsinmi?`))) return;
          await api('POST', '/billing/cancel'); await refreshMe(); toast('Obuna bekor qilindi'); renderMain();
        }) }, 'Obunani bekor qilish'),
    ];
  } else {
    status = h('span', { class: 'badge ' + (b.on_trial ? 'accent' : 'bad') }, b.on_trial ? `Sinov · ${b.days_left} kun` : 'Faol emas');
    body = [
      h('div', { class: 'price' }, String(b.price) + ' ⭐ ', h('small', {}, '/ 30 kun')),
      h('ul', { class: 'perks' },
        h('li', {}, 'Cheksiz javob qoralamalari'),
        h('li', {}, 'Shaxsiy qoidalar va uslublar'),
        h('li', {}, 'Har oy avtomatik yangilanadi, istalgan payt bekor qilinadi')),
      b.on_trial ? h('div', { class: 'progress' }, h('i', { 'data-w': pct })) : null,
      h('button', { class: 'btn block', onclick: busy(pay) }, `Obuna bo'lish · ${b.price} ⭐`),
      h('p', { class: 'muted small center' }, "To'lov Telegram Stars orqali — karta ma'lumotlari bizga kelmaydi."),
    ];
  }
  const card = h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', {}, 'Obuna'), status), body);
  // progress kengligi: inline style CSP'da taqiqlangan, shuning uchun CSSOM orqali.
  card.querySelectorAll('[data-w]').forEach((el) => { el.style.width = el.getAttribute('data-w') + '%'; });
  return card;
}

async function pay() {
  const { link } = await api('POST', '/billing/invoice');
  if (!tg || !tg.openInvoice) { if (tg && tg.openTelegramLink) tg.openTelegramLink(link); return; }
  tg.openInvoice(link, async (status) => {
    if (status === 'paid') {
      toast("To'lov qabul qilindi — obuna faollashmoqda…");
      // Obuna server tomonda Telegram tasdig'i (successful_payment) kelgach yoqiladi.
      for (let i = 0; i < 10; i++) {
        await new Promise((r) => setTimeout(r, 1500));
        try { await refreshMe(); } catch (_) { /* */ }
        if (state.me.billing && state.me.billing.paid) break;
      }
      renderMain();
    } else if (status === 'failed') {
      toast("To'lov amalga oshmadi", true);
    }
  });
}

boot();
