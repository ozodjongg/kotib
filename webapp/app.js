'use strict';
// Barcha dinamik matn textContent orqali qo'yiladi (innerHTML ishlatilmaydi) — kontakt xabarlari XSS bermaydi.

const tg = window.Telegram && window.Telegram.WebApp;
if (tg) { tg.ready(); tg.expand(); }
const root = document.getElementById('app');

const PERSONA_LABELS = {
  neutral: "Do'stona (standart)", joke: 'Hazil ruhida', son: "O'g'il nomidan",
  formal: 'Rasmiy', brief: 'Juda qisqa', custom: "O'z ko'rsatmam",
};

const state = { me: null, settings: null, contacts: null, drafts: [], tab: 'drafts', login: { step: 'idle' } };

// ---------- yordamchilar ----------
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (v === true) el.setAttribute(k, '');
    else if (v !== false && v != null) el.setAttribute(k, v);
  }
  for (const kid of kids.flat()) if (kid != null) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  return el;
}

async function api(method, path, body) {
  const res = await fetch('/api' + path, {
    method,
    headers: { 'Authorization': 'tma ' + (tg ? tg.initData : ''), 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  let data = null;
  try { data = await res.json(); } catch (_) { /* bo'sh javob */ }
  if (!res.ok) throw new Error((data && data.error) || ('Xatolik ' + res.status));
  return data;
}

function confirmBox(text) {
  return new Promise((resolve) => {
    if (tg && tg.showConfirm) tg.showConfirm(text, resolve); else resolve(window.confirm(text));
  });
}
function toast(text) { if (tg && tg.showAlert) tg.showAlert(text); else alert(text); }
const rid = () => 'r' + Math.random().toString(36).slice(2, 10);

// ---------- ekranlar ----------
async function boot() {
  if (!tg || !tg.initData) {
    root.replaceChildren(h('p', { class: 'err' }, "Ilovani Telegram ichidan oching."));
    return;
  }
  try {
    state.me = await api('GET', '/me');
    state.settings = await api('GET', '/settings');
    if (!Array.isArray(state.settings.rules)) state.settings.rules = [];
    if (!state.settings.quiet_hours) state.settings.quiet_hours = { enabled: false, start: '23:00', end: '07:00', tz: 'Asia/Tashkent' };
  } catch (e) {
    root.replaceChildren(h('p', { class: 'err' }, "Ochilmadi: " + e.message + ". Ilovani Telegram ichidan oching."));
    return;
  }
  render();
}

function render() {
  if (!state.me.consented) return renderConsent();
  if (state.me.status === 'pending' || state.me.status === 'revoked') return renderConnect();
  renderMain();
}

function renderConsent() {
  root.replaceChildren(
    h('h2', {}, 'Kotib'),
    h('div', { class: 'card' },
      h('p', {}, "Kotib sizning Telegram akkauntingiz nomidan kiruvchi xabarlarga javob qoralamasini tayyorlaydi."),
      h('p', {}, "Buning uchun akkauntingizga ulanish (sessiya) kerak bo'ladi. Sessiya shifrlangan holda saqlanadi; xohlagan paytda ilovadan uzib, barcha ma'lumotni o'chirishingiz mumkin."),
      h('p', { class: 'muted' }, "Muhim: Telegram qoidalari avtomatik javoblarni cheklashi mumkin; akkauntingiz cheklanish xavfi doim mavjud. Standart rejim — «qoralama»: hech narsa siz tasdiqlamaguncha yuborilmaydi. Suhbatdoshlaringizga javoblar siz nomidan ketishini hisobga oling."),
    ),
    h('button', { onclick: async () => {
      try { await api('POST', '/consent', { accepted: true }); state.me = await api('GET', '/me'); render(); }
      catch (e) { toast(e.message); }
    } }, 'Tushundim, roziman'),
  );
}

function renderConnect() {
  const st = state.login;
  const err = st.error ? h('p', { class: 'err' }, st.error) : null;
  let body;
  if (st.step === 'wait_code') {
    const code = h('input', { type: 'tel', inputmode: 'numeric', autocomplete: 'one-time-code', placeholder: 'Telegram yuborgan kod' });
    body = [h('p', {}, "Telegram'ga kelgan kodni kiriting."), code,
      h('p', { class: 'muted' }, "Kodni chatda yozmang yoki boshqaga yubormang — faqat shu yerga kiriting."),
      h('button', { onclick: () => submit('/login/code', { code: code.value }) }, 'Tasdiqlash')];
  } else if (st.step === 'wait_password') {
    const pw = h('input', { type: 'password', placeholder: 'Ikki bosqichli parol (2FA)', autocomplete: 'off' });
    body = [h('p', {}, "Akkauntingizda 2FA parol bor. Parol saqlanmaydi, faqat bir marta ishlatiladi."), pw,
      h('button', { onclick: () => submit('/login/password', { password: pw.value }) }, 'Tasdiqlash')];
  } else if (st.step === 'connecting') {
    body = [h('p', {}, 'Ulanmoqda…')];
    setTimeout(pollLogin, 1500);
  } else if (st.step === 'done') {
    body = [h('p', {}, 'Ulandi!'), h('button', { onclick: async () => { state.me = await api('GET', '/me'); render(); } }, 'Davom etish')];
  } else {
    const phone = h('input', { type: 'tel', placeholder: '+998901234567', autocomplete: 'tel' });
    body = [h('p', {}, "Telegram akkauntingiz telefon raqami:"), phone,
      h('button', { onclick: async () => {
        try { await api('POST', '/login/phone', { phone: phone.value }); state.login = { step: 'connecting' }; render(); }
        catch (e) { toast(e.message); }
      } }, 'Kod yuborish')];
  }
  root.replaceChildren(h('h2', {}, 'Akkauntni ulash'), h('div', { class: 'card' }, body), err);
}

async function submit(path, payload) {
  try { await api('POST', path, payload); state.login = { step: 'connecting' }; render(); }
  catch (e) { toast(e.message); }
}

async function pollLogin() {
  try {
    state.login = await api('GET', '/login/state');
    if (state.login.step === 'done') state.me = await api('GET', '/me');
  } catch (_) { /* keyingi urinishda */ }
  if (!state.me.consented || !(state.me.status === 'pending' || state.me.status === 'revoked' || state.login.step === 'done')) return;
  renderConnect();
}

function renderMain() {
  const tabs = [['drafts', 'Qoralamalar'], ['settings', 'Sozlamalar'], ['rules', 'Qoidalar'], ['safety', 'Xavfsizlik']];
  const bar = h('div', { class: 'tabs' }, tabs.map(([k, label]) =>
    h('button', { class: state.tab === k ? 'on' : '', onclick: () => { state.tab = k; renderMain(); } }, label)));
  const view = h('div', {});
  root.replaceChildren(bar, statusLine(), view);
  ({ drafts: viewDrafts, settings: viewSettings, rules: viewRules, safety: viewSafety })[state.tab](view);
}

function statusLine() {
  const s = state.me.status;
  const cls = s === 'active' ? 'ok' : (s === 'broken' ? 'bad' : '');
  const names = { active: 'Faol', paused: "To'xtatilgan", broken: "Xavfsizlik uchun to'xtatilgan", pending: 'Ulanmagan' };
  return h('p', {}, h('span', { class: 'badge ' + cls }, names[s] || s),
    state.me.note ? h('span', { class: 'muted' }, ' ' + state.me.note) : null);
}

// ---------- Qoralamalar ----------
async function viewDrafts(view) {
  view.append(h('p', { class: 'muted' }, 'Yuklanmoqda…'));
  try { state.drafts = await api('GET', '/drafts'); } catch (e) { view.replaceChildren(h('p', { class: 'err' }, e.message)); return; }
  if (!state.drafts.length) { view.replaceChildren(h('p', { class: 'muted' }, "Hozircha qoralama yo'q.")); return; }
  view.replaceChildren(...state.drafts.map(draftCard));
}

function draftCard(d) {
  const text = h('textarea', {}, d.reply);
  const card = h('div', { class: 'card' },
    h('div', { class: 'in msg' }, d.incoming),
    h('div', { class: 'muted' }, d.reason),
    text,
    h('div', { class: 'row' },
      h('button', { onclick: async (ev) => {
        ev.target.disabled = true;
        try { await api('POST', `/drafts/${d.id}/send`, { reply: text.value }); card.remove(); }
        catch (e) { toast(e.message); ev.target.disabled = false; }
      } }, 'Yuborish'),
      h('button', { class: 'secondary', onclick: async () => {
        try { await api('POST', `/drafts/${d.id}/reject`); card.remove(); } catch (e) { toast(e.message); }
      } }, 'Rad etish')));
  return card;
}

// ---------- Sozlamalar ----------
function field(label, control) { return h('div', { class: 'row' }, h('label', {}, label), control); }

function personaSelect(value, onchange) {
  const sel = h('select', { onchange: (e) => onchange(e.target.value) },
    Object.entries(PERSONA_LABELS).map(([k, v]) => h('option', { value: k, selected: k === value }, v)));
  return sel;
}

function viewSettings(view) {
  const s = state.settings;
  const mode = h('select', { onchange: async (e) => {
    if (e.target.value === 'auto') {
      const ok = await confirmBox("Avto-rejimda javoblar siz tasdiqlamasdan yuboriladi. Telegram cheklovi xavfi oshadi. Yoqilsinmi?");
      if (!ok) { e.target.value = s.mode; return; }
    }
    s.mode = e.target.value;
  } },
    [['draft', "Qoralama (tavsiya etiladi)"], ['auto', 'Avtomatik'], ['off', "O'chiq"]].map(([k, v]) => h('option', { value: k, selected: k === s.mode }, v)));
  const custom = h('textarea', { maxlength: 600, placeholder: "Masalan: qisqa va samimiy yoz, ismimni 'Aziz' deb ayt…" }, s.default_custom || '');
  custom.addEventListener('input', () => { s.default_custom = custom.value; });
  const quietOn = h('input', { type: 'checkbox', checked: !!s.quiet_hours.enabled, onchange: (e) => { s.quiet_hours.enabled = e.target.checked; } });

  view.append(
    field('Rejim', mode),
    field('Ismingiz (javoblarda kontekst uchun)', h('input', { type: 'text', value: s.owner_name || '', maxlength: 64, oninput: (e) => { s.owner_name = e.target.value; } })),
    field('Til', h('select', { onchange: (e) => { s.language = e.target.value; } },
      [['auto', 'Avto (kontakt tilida)'], ['uz', "O'zbekcha"], ['ru', 'Ruscha'], ['en', 'Inglizcha']].map(([k, v]) => h('option', { value: k, selected: k === s.language }, v)))),
    field('Standart uslub', personaSelect(s.default_persona, (v) => { s.default_persona = v; })),
    field("O'z ko'rsatmangiz (uslub «O'z ko'rsatmam» bo'lsa)", custom),
    h('div', { class: 'row' }, h('label', {}, h('input', { type: 'checkbox', checked: !!s.reply_unknown, onchange: (e) => { s.reply_unknown = e.target.checked; } }), " Kontaktimda yo'qlarga ham javob berish (tavsiya etilmaydi)")),
    h('div', { class: 'row' }, h('label', {}, h('input', { type: 'checkbox', checked: !!s.allow_links, onchange: (e) => { s.allow_links = e.target.checked; } }), ' Javobda havolalarga ruxsat (tavsiya etilmaydi)')),
    h('div', { class: 'row' }, h('label', {}, quietOn, ' Tinch soatlar (shu vaqtda javob yozilmaydi)')),
    h('div', { class: 'row' },
      h('input', { type: 'time', value: s.quiet_hours.start, onchange: (e) => { s.quiet_hours.start = e.target.value; } }),
      h('input', { type: 'time', value: s.quiet_hours.end, onchange: (e) => { s.quiet_hours.end = e.target.value; } })),
    saveButton(),
  );
}

function saveButton() {
  return h('button', { onclick: async (ev) => {
    ev.target.disabled = true;
    try { await api('PUT', '/settings', state.settings); toast('Saqlandi'); }
    catch (e) { toast(e.message); }
    ev.target.disabled = false;
  } }, 'Saqlash');
}

// ---------- Qoidalar ----------
async function viewRules(view) {
  const s = state.settings;
  view.append(h('p', { class: 'muted' }, "Qoida: tanlangan kontaktlarga boshqacha uslub. Birinchi mos kelgan qoida ishlaydi. Kontaktda yo'qlarga baribir javob berilmaydi (sozlamada yoqilmasa)."));
  if (state.contacts === null) {
    try { state.contacts = await api('GET', '/contacts'); }
    catch (e) { state.contacts = []; view.append(h('p', { class: 'err' }, "Kontaktlar yuklanmadi: " + e.message)); }
  }
  const list = h('div', {});
  const redraw = () => list.replaceChildren(...s.rules.map((r, i) => ruleCard(r, i, redraw)));
  redraw();
  view.append(list,
    h('button', { class: 'secondary', onclick: () => {
      if (s.rules.length >= 50) return toast('Qoidalar soni cheklangan');
      s.rules.push({ id: rid(), title: 'Yangi qoida', match: { user_ids: [] }, persona: 'neutral' }); redraw();
    } }, '+ Qoida qo\'shish'),
    h('div', { class: 'row' }, saveButton()));
}

function ruleCard(r, i, redraw) {
  const search = h('input', { type: 'text', placeholder: 'Kontakt qidirish…' });
  const box = h('div', { class: 'clist' });
  const drawContacts = () => {
    const q = search.value.trim().toLowerCase();
    box.replaceChildren(...state.contacts.filter((c) => !q || c.name.toLowerCase().includes(q)).slice(0, 200).map((c) =>
      h('label', {}, h('input', { type: 'checkbox', checked: (r.match.user_ids || []).includes(c.id), onchange: (e) => {
        const set = new Set(r.match.user_ids || []);
        e.target.checked ? set.add(c.id) : set.delete(c.id);
        r.match.user_ids = [...set];
      } }), c.name)));
  };
  search.addEventListener('input', drawContacts);
  drawContacts();

  const custom = h('textarea', { maxlength: 600, placeholder: "O'z ko'rsatmangiz" }, r.custom || '');
  custom.addEventListener('input', () => { r.custom = custom.value; });
  const modeSel = h('select', { onchange: (e) => { r.mode = e.target.value || undefined; } },
    [['', 'Umumiy rejim'], ['draft', 'Qoralama'], ['auto', 'Avtomatik'], ['off', "Javob yozma"]].map(([k, v]) => h('option', { value: k, selected: (r.mode || '') === k }, v)));

  return h('div', { class: 'card' },
    field('Nomi', h('input', { type: 'text', value: r.title || '', maxlength: 60, oninput: (e) => { r.title = e.target.value; } })),
    field('Kontaktlar', h('div', { class: 'full' }, search, box)),
    field('Uslub', personaSelect(r.persona || 'neutral', (v) => { r.persona = v; })),
    field("O'z ko'rsatma (uslub «O'z ko'rsatmam» bo'lsa)", custom),
    field('Rejim bu kontaktlar uchun', modeSel),
    h('div', { class: 'row' },
      h('label', {}, h('input', { type: 'checkbox', checked: !!r.disabled, onchange: (e) => { r.disabled = e.target.checked; } }), " O'chirib qo'yish"),
      h('button', { class: 'danger', onclick: () => { state.settings.rules.splice(i, 1); redraw(); } }, "O'chirish")));
}

// ---------- Xavfsizlik ----------
function viewSafety(view) {
  const paused = state.me.status === 'paused' || state.me.status === 'broken';
  view.append(
    h('div', { class: 'card' },
      h('p', {}, paused ? "Kotib hozir ishlamayapti." : 'Kotib ishlayapti.'),
      paused
        ? h('button', { onclick: async () => { try { await api('POST', '/resume'); state.me = await api('GET', '/me'); renderMain(); } catch (e) { toast(e.message); } } }, 'Davom ettirish')
        : h('button', { class: 'secondary', onclick: async () => { try { await api('POST', '/pause'); state.me = await api('GET', '/me'); renderMain(); } catch (e) { toast(e.message); } } }, "To'xtatish")),
    h('div', { class: 'card' },
      h('p', {}, "Akkauntni uzish va barcha ma'lumotni o'chirish: Telegram sessiyasi tugatiladi, sozlamalar va qoralamalar o'chadi."),
      h('p', { class: 'muted' }, "Qo'shimcha: Telegram → Sozlamalar → Qurilmalar bo'limidan istalgan vaqtda sessiyani o'zingiz ham uzishingiz mumkin."),
      h('button', { class: 'danger', onclick: async () => {
        if (!(await confirmBox("Hamma narsa o'chiriladi. Davom etasizmi?"))) return;
        try { await api('DELETE', '/account', { confirm: 'DELETE' }); toast("O'chirildi"); if (tg) tg.close(); }
        catch (e) { toast(e.message); }
      } }, "Akkauntni uzish va o'chirish")));
}

boot();
