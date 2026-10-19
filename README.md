# Telegram Kotib (Go)

Mijozning **o'z Telegram akkaunti** (MTProto sessiya) orqali ishlaydigan AI kotib.
Boshqaruv: Telegram bot + **Mini App**. Model: Gemini (REST). Til: Go.

## Tuzilma

```
cmd/secretary/        main, env sozlamalari, graceful shutdown
internal/crypto/      AES-256-GCM envelope shifrlash (session uchun), kalit rotatsiyasi
internal/guard/       kiruvchi xabar tasnifi (injection / sezgir mavzu), chiqish filtri
internal/ratelimit/   token-bucket (global/chat/kunlik) + "odamga o'xshash" kechikish
internal/config/      sozlamalar, qoidalar, kontakt bo'yicha qaror (Resolve), tinch soatlar
internal/llm/         Gemini REST klienti + system prompt, vazifa agenti (function calling), internet qidiruvi
internal/userbot/     pipeline (Telegram'dan mustaqil), manager, login, gotd.go (build tag)
internal/miniapp/     initData HMAC auth + HTTP API
internal/store/       Store interfeysi, Postgres, Memory (dev)
internal/bot/         boshqaruv boti (/start, bildirishnoma)
internal/billing/     Telegram Stars obunasi: sinov, to'lov tasdig'i, bekor qilish, refund, eslatmalar
internal/google/      Google: OAuth (PKCE), Calendar (band vaqt, tadbir), Contacts (eslatma), Tasks
internal/ics/         Google'siz taqvim: .ics fayl va imzolangan havola
internal/schedule/    rejalashtirilgan AI vazifalar: jadval (bir marta/soat/kun/ish kunlari/hafta), bajaruvchi
internal/plugin/      plaginlar (connector'lar): internet qidiruvi, YouTube, ob-havo, valyuta kursi; shifrlangan sozlama
internal/botcmd/      bot buyruqlari (/status, /pause, /obuna, admin /stats, /refund) va to'lov hodisalari
webapp/               Mini App (vanilla JS, innerHTML ishlatilmaydi; iliq fil suyagi/terrakota dizayn, yorug'/qorong'i)
migrations/           Postgres sxema (binary ichiga joylangan, ishga tushganda avtomatik qo'llanadi)
Dockerfile            distroless, root'siz production image
docker-compose.yml    ilova + Postgres namunasi
.github/workflows/    CI: gofmt, vet, race-testlar (Postgres bilan), production build, govulncheck
```

## Yig'ish

Go 1.26+ kerak (`go.mod`dagi `toolchain` avtomatik yuklanadi). Bog'liqliklar `go.sum` bilan qotirilgan.

```bash
go test -race ./...                                  # barcha testlar
TEST_DATABASE_URL=postgres://... go test ./internal/store   # + Postgres integratsiya testi
go build -tags gotd -o secretary ./cmd/secretary     # production (Telegram + Postgres)
```

`-tags gotd`siz yig'ilgan binary Telegram'ga ulanmaydi (faqat test/CI uchun).
`internal/store/postgres.go` va migratsiyalar haqiqiy Postgres 16 bilan sinalgan.
**`internal/userbot/gotd.go` gotd v0.162 bilan kompilyatsiya qilinadi, lekin haqiqiy Telegram akkaunt bilan hali sinalmagan** —
birinchi ishga tushirishda ehtiyotkorlik bilan, o'z akkauntingizda sinang.

### Docker

```bash
cp .env.example .env    # to'ldiring (POSTGRES_PASSWORD ham)
docker compose up -d --build
```

Ilova faqat `127.0.0.1:8080` da tinglaydi — HTTPS'ni Caddy/nginx tugatsin, masalan Caddy:
`secretary.example.com { reverse_proxy 127.0.0.1:8080 }`.

### Railway

Repoda `railway.json` bor: Dockerfile bilan yig'iladi, healthcheck `/healthz`, **1 ta replica**.

1. New Project → Deploy from GitHub repo; + New → Database → PostgreSQL.
2. App Variables: `DATABASE_URL=${{Postgres.DATABASE_URL}}`, qolganlari pastdagi jadvaldan (`PORT`ni Railway o'zi beradi).
3. Settings → Networking → Generate Domain; domenni `MINIAPP_URL`ga qo'ying va @BotFather → Menu Button'ga ulang.

**Replica sonini oshirmang**: ikki nusxa bitta Telegram sessiyasiga ulanib, ban xavfini oshiradi
(login holati ham xotirada). Deploy paytida eski va yangi nusxa bir necha soniya birga ishlaydi —
bitta sessiyaga ikki joydan ulanilsa Telegram `AUTH_KEY_DUPLICATED` bilan sessiyani butunlay bekor qiladi.
Buning oldini olish uchun ilova Postgres advisory lock'ini ushlaydi: yangi nusxa HTTP'ni darhol ochadi
(healthcheck o'tadi), lekin Telegram sessiyalariga faqat eski nusxa to'liq to'xtagach ulanadi.
Sessiya bekor qilinsa, akkaunt "uzilgan" holatga o'tadi va Mini App qayta ulashni so'raydi. Railway IP'lari umumiy — Telegram login'ni bloklasa, `TG_PROXY` qo'shing.

## Brending

`branding/` — logo (gap pufagi ichida qalam uchi: "siz nomingizdan yozadi"), terrakota/fil suyagi ranglarida.

| Fayl | Qayerda ishlatiladi |
|---|---|
| `logo.svg` | asosiy belgi (vektor) |
| `logo-640.png` | @BotFather → `/setuserpic` (bot avatari) |
| `logo-1024.png`, `logo-512.png` | do'kon, ijtimoiy tarmoqlar, taqdimot |
| `logo-192.png`, `logo-64.png`, `logo-32.png` | ilova ikonkasi, favicon |
| `wordmark.svg/.png`, `wordmark-dark.svg/.png` | belgi + "Kotib" yozuvi (yorug' / qorong'i fon) |
| `preview.png` | umumiy ko'rinish |

Wordmark shrifti — Source Serif 4 (SIL Open Font License), matn vektor konturga aylantirilgan: shrift o'rnatish shart emas.

## Sozlash (env)

| O'zgaruvchi | Izoh |
|---|---|
| `TG_APP_ID`, `TG_APP_HASH` | https://my.telegram.org → API development tools |
| `BOT_TOKEN` | @BotFather; Mini App URL'ini bot menyusiga ulang |
| `MINIAPP_URL` | Mini App'ning HTTPS manzili (reverse proxy orqali) |
| `GEMINI_API_KEY`, `GEMINI_MODEL` | Model nomini Google AI Studio'dan aniq nusxalang |
| `MASTER_KEY` | `openssl rand -base64 32` — session shifrlash kaliti. **Bazada saqlanmaydi** |
| `DATABASE_URL` | Postgres; sxema ishga tushganda avtomatik yaratiladi/yangilanadi |
| `TG_PROXY` | ixtiyoriy `socks5://...` — barqaror IP |
| `PRICE_STARS` | **Advanced** tarif oylik narxi (Telegram Stars, 1–10000), masalan `150`. Bo'sh/0 — to'lov o'chiq, xizmat bepul |
| `PRICE_PRO_STARS` | **Pro** tarif oylik narxi, masalan `250` (`PRICE_STARS`dan katta). Bo'sh/0 — bitta tarif: obunada hamma funksiya ochiq |
| `TRIAL_DAYS` | bepul sinov kunlari (default 7) |
| `TRIAL_DAILY_LIMIT` | sinovda kuniga ko'pi bilan nechta javob tayyorlanadi (default 50; 0 — cheksiz). To'langan obunada cheklov yo'q |
| `ADMIN_IDS` | admin Telegram ID'lari, vergul bilan: `/stats`, `/refund`; adminlar uchun kotib bepul |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | ixtiyoriy: Google integratsiyasi (pastda). Bo'sh — o'chiq |
| `GOOGLE_REDIRECT_URL` | ixtiyoriy; default `<MINIAPP_URL domeni>/oauth/google/callback` |
| `LISTEN` | aniq manzil (masalan `127.0.0.1:8080`); berilmasa `PORT` ishlatiladi |
| `PORT` | platforma beradi (Railway); `0.0.0.0:$PORT` da tinglanadi. Ikkalasi ham bo'lmasa — `127.0.0.1:8080` |
| `STATIC_DIR` | Mini App fayllari, default `./webapp` |
| `DEV_MEMORY=1` | faqat dev: xotirada saqlash |

## Suhbat konteksti

Har javobdan oldin chatdagi oxirgi xabarlar (egasi va kontaktniki) modelga beriladi: **obunada 20 ta,
sinovda 8 ta** (`billing.HistoryPaid` / `HistoryTrial`). Bitta chatdagi xabarlar qat'iy navbat bilan
qayta ishlanadi: keyingi javob oldingisi yuborilgandan keyin tayyorlanadi va uni tarixda ko'radi
(bir xil javob takrorlanmaydi, tartib buzilmaydi).

## Kechki xulosa, va'dalar va tejalgan vaqt

- **Kechki xulosa** — har kuni soat 21:00 da (egasining vaqt zonasi) bot yuboradi: tayyorlangan va
  yuborilgan javoblar, tasdiq kutayotgan qoralamalar, «keyinroq yozaman» deyilgan kontaktlar va oylik
  tejalgan vaqt. Faoliyat bo'lmagan kuni yuborilmaydi; kuniga bir marta (audit `digest_sent`).
- **Va'dalarni kuzatish** — kotib (avtomatik yoki tasdiqlangan qoralama) «keyinroq yozaman» kabi
  javob yuborsa, 3 soatdan keyin chat tekshiriladi: egasining oxirgi xabari hali o'sha javob bo'lsa —
  bot eslatadi, egasi o'zi yozgan bo'lsa — jim yopiladi. Xabar matni saqlanmaydi, faqat xesh (`followups`).
- **Tejalgan vaqt** — kotib orqali yuborilgan har javob ≈ 1 daqiqa (`store.SavedPerReply`).
  Mini App bosh sahifasida, `/status` va xulosada ko'rsatiladi.

Ikkala bildirishnoma Sozlamalarda o'chiriladi. Tekshiruv har 5 daqiqada, faqat bitta nusxada (gate).

## Kunlik yordamchi

Sozlamalar → «Kunlik yordamchi». Hammasi har 5 daqiqalik tekshiruvda, kuniga bir marta (audit bo'yicha).

- **AI xulosa: «Bugun nima bo'ldi»** (Pro, default o'chiq, `smart_digest`) — kechki xulosaga bugungi yozishmalar mazmuni
  qo'shiladi: har suhbatga bir qator (kim nima so'radi, nima kelishildi), javob kutilayotganlari ❗ bilan, oxirida
  «Qilish kerak» ro'yxati. Xabarlar shu payt Telegram'dan o'qiladi va Gemini'ga yuboriladi; xulosa faqat bot chatiga
  keladi, bazada saqlanmaydi.
- **Ertalabki brifing** (default o'chiq, `morning_brief`) — 8:00 da: kechagi 21:00 dan beri kim yozdi, kim javob kutyapti,
  kutayotgan qoralamalar, bugungi taqvimdagi band vaqtlar (Google, Pro) va — AI xulosa yoqilgan bo'lsa — tungi xabarlar xulosasi.
- **Shoshilinch xabarlar** (default yoqiq) — «shoshilinch», «srochno/срочно», «kasalxona», «urgent» kabi xabarlar haqida
  bot darhol yozadi, **tinch soatlarda ham** (bitta chatdan 30 daqiqada bir marta). Xabar matni bot chatiga faqat
  «Qoralamalar bot chatida» yoqilgan bo'lsa qo'shiladi.
- **Javobsiz qolganlar** (default yoqiq, 4 soat; 1/2/4/8/24) — kontakt yozgan, lekin na egasi, na kotib javob bergan
  bo'lsa eslatadi (bir xabar uchun bir marta, tinch soatlarda emas, qoralamasi kutayotgan chatlar bundan mustasno).
  Kechki xulosada ham «❗ Javob kutayotganlar» qatori chiqadi.
- **Ovozli xabar matni** (Pro, `transcribe_voice`) — kontakt ovozli xabari (5 daqiqagacha) matnga o'girilib bot chatiga keladi.
- **Tug'ilgan kun tabriklari** (Pro, Google ulangan) — 9:00 da Google Contacts'da bugun tug'ilganlar uchun egasi uslubida
  tabrik **qoralamasi** tayyorlanadi (Telegram kontakti telefon yoki ism bo'yicha topiladi). Faqat egasi tasdiqlasa yuboriladi.
- **`/sor <savol>`** (Pro) — «/sor Aziz bilan nima kelishdik?»: bot savoldagi kontaktni topadi, oxirgi 100 xabarni o'qib javob beradi.
  Qoida bilan kotibdan yopilgan chatlar o'qilmaydi.

Bularning hammasi uchun `chat_activity` jadvalida faqat meta saqlanadi: chat, kontakt ismi, oxirgi kiruvchi/chiquvchi
xabar vaqti (matn yo'q; 30 kun faolligi bo'lmasa o'chiriladi). Egasi qoida bilan kotibni chetlatgan chatlar (`private`)
AI xulosaga ham, eslatmalarga ham kirmaydi.

## O'qitish rejimi: «O'zimdek» uslubi (Pro)

Kotib egasining haqiqiy yozishmalaridan uslubini o'rganadi va xuddi o'zidek javob yozadi (Sozlamalar → «O'qitish rejimi»).

1. **Uslubimni o'rganish** — eng faol 15 ta shaxsiy chatning oxirgi xabarlari o'qiladi va Gemini uslub profilini yozadi:
   javob uzunligi, salomlashish, murojaat (siz/sen, aka, onajon), imlo, emoji, sevimli iboralar, kimga qanday yozishi.
   Profil matni Mini App'da ko'rinadi va tahrirlanadi; yozishmalarning o'zi saqlanmaydi. Uslub sifatida **«O'zimdek»**
   (`mine`) tanlanadi — umumiy yoki qoida bo'yicha.
2. **Tahrirlardan o'rganish** (`learn`) — egasi qoralamani o'zgartirib yuborsa (Mini App yoki bot), «kotib yozgani → egasi
   yuborgani» juftligi saqlanadi. Har kuni soat 3:00 da (kamida 3 juftlik bo'lsa) profil shu farqlar asosida yangilanadi va
   **moslik bahosi** (0–100%) hisoblanadi. Juftliklar ishlatilgach o'chiriladi, aks holda 7 kunda.
3. **Soya rejimi** (7 kun, 14 kungacha) — kotib har xabarga javobni **jim** tayyorlaydi: hech narsa yubormaydi, qoralama ham
   ko'rsatmaydi. Egasi o'zi javob yozgach (Telegram'ning istalgan qurilmasidan), ikkalasi juftlik sifatida solishtiriladi.
   Muddat tugagach bot natijani yuboradi: nechta javob tayyorlandi, o'rtacha moslik va **[⚡️ Avto rejim] [✍️ Qoralamada qolish]**
   tugmalari (ikkalasi ham «O'zimdek» uslubini yoqadi).

Kotib o'zi yuborgan xabarlar «egasi yozdi» deb hisoblanmaydi (10 daqiqalik xesh belgisi).

## Bilim bazasi va kontakt eslatmalari

- **Bilim bazasi** (Sozlamalar, 4000 belgigacha) — narxlar, ish vaqti, xizmatlar, manzil. Kotib narx/vaqt savollariga faqat
  shundan javob beradi, bazada yo'q narsani o'ylab topmaydi. Do'kon va biznes akkauntlari uchun.
- **Kontakt eslatmalari** (Qoidalar → «Kontakt eslatmalari», har biri 300 belgigacha) — «onam, «siz» deb yoz»,
  «loyiha X bo'yicha hamkor»: kotib shu kontaktga javob yozganda hisobga oladi (Google Contacts eslatmasidan mustaqil).

## Rasmli xabarlar (Pro)

Sozlamalar → «Rasmlarga ham» (default o'chiq): kontakt yuborgan rasm (eng kattasi 1280 px, 1 MB gacha) Gemini'ga
yuboriladi va kotib rasm mazmuniga javob qoralamasini tayyorlaydi. Rasmdagi yozuv matn filtridan o'tolmagani uchun javob
**har doim faqat qoralama**. O'chiq bo'lsa, rasm izohi bo'lsa — faqat izohga javob yoziladi.

## To'lov: Telegram Stars obunasi

### Tariflar

| | Advanced (`PRICE_STARS`) | Pro (`PRICE_PRO_STARS`) |
|---|---|---|
| Javob qoralamalari | cheksiz | cheksiz |
| Suhbat konteksti | 20 xabar | 40 xabar |
| Avto rejim, qoidalar, holatlar, kechki xulosa, va'dalar, .ics taqvim | ✅ | ✅ |
| 🎤 Ovozli xabarlarga javob | — | ✅ |
| 📅 Google Calendar / Contacts / Tasks | — | ✅ |
| 📱 Telefon avtomatikasi (Shortcuts / MacroDroid havolasi) | — | ✅ |
| 👥 Bir nechta Telegram akkaunt (+2 ta) | — | ✅ |
| 📊 Kengaytirilgan statistika va haftalik hisobot | — | ✅ |
| ⏰ Rejalashtirilgan AI vazifalar | ✅ | ✅ |
| 🧩 Plaginlar (internet qidiruvi, YouTube, ob-havo, valyuta kursi) | — | ✅ |
| 🛎 Shoshilinch xabarlar, javobsiz qolganlar, ertalabki brifing, bilim bazasi, kontakt eslatmalari | ✅ | ✅ |
| 📝 AI xulosa, 🪞 o'qitish/soya rejimi, 🖼 rasmlar, 🎤 ovoz matni, 🎂 tabriklar, `/sor` | — | ✅ |

- **Sinov** davrida Pro funksiyalari ochiq (kunlik limit va 8 xabarlik kontekst bilan) — mijoz tatib ko'rib tanlaydi.
- **Tarif almashtirish** (Advanced ↔ Pro): Mini App → Hisob yoki `/obuna`. Yangi tarif to'langach eski obunaning avtomatik
  yangilanishi o'chiriladi, qolgan kunlari narx nisbatida (masalan, Advanced'ning 20 kuni → Pro'da 12 kun) bonus kun sifatida
  yangi davrdan keyin qo'shiladi. Pro funksiyalari yopiq bo'lsa API `402` qaytaradi.
- **Eski obunachilar** (tariflar yoqilishidan oldin to'laganlar) Pro sifatida qoladi — funksiyalarini yo'qotmaydi; oylik
  yangilanishi eski narxda davom etadi. Referal bonus kunlari oxirgi to'langan tarif bo'yicha (hech to'lamagan — Advanced).

`PRICE_STARS` berilsa, to'lov yoqiladi:

- Rozilik berilganda **bepul sinov** (`TRIAL_DAYS`) boshlanadi. Sinov bir marta beriladi: akkauntni o'chirib qayta ochish yangi sinov bermaydi.
  Sinovda kuniga ko'pi bilan `TRIAL_DAILY_LIMIT` (default 50) ta javob tayyorlanadi; limit Toshkent vaqti bilan yarim tunda yangilanadi, hisob Postgres'da (`usage_daily`) — qayta ishga tushish uni tozalamaydi.
  To'lov yoqilishidan oldin ro'yxatdan o'tgan mijozlar ham sinovni oladi (xizmat ogohlantirishsiz to'xtamaydi). `ADMIN_IDS` dagilar uchun bepul.
- Mini App → **Hisob → Obuna bo'lish**: `createInvoiceLink` (XTR, `subscription_period` = 30 kun) → `Telegram.WebApp.openInvoice`.
  Bot buyrug'i `/obuna` ham to'lov havolasini beradi.
- Telegram har 30 kunda pulni o'zi yechadi; har to'lov `successful_payment` bo'lib keladi va obuna `subscription_expiration_date` gacha uzaytiriladi.
- Obuna tugasa, kotib yangi xabarlarga javob tayyorlamaydi (model umuman chaqirilmaydi) va bot bir marta eslatadi. Tugashidan 24 soat oldin ham eslatma keladi.
- **Bekor qilish** (Mini App): `editUserStarSubscription` — to'langan muddat oxirigacha ishlaydi, keyin yangilanmaydi. Qayta yoqish mumkin. Akkaunt o'chirilsa — avtomatik bekor qilinadi.
- **Qaytarish** (admin): `/refund <telegram_payment_charge_id>` — `refundStarPayment`, shu to'lov bergan muddat darhol tugaydi.
  `charge_id` ni `payments` jadvalidan yoki logdan olasiz. Telegram tomonidan qaytarilgan to'lovlar (`refunded_payment`) ham avtomatik qayd etiladi.
- Yig'ilgan Stars: @BotFather → bot → **Balance** (Fragment orqali chiqariladi).

To'lov xavfsizligi:
- Obuna faqat Telegram yuborgan `successful_payment` asosida yoqiladi — Mini App yoki mijoz "to'ladim" deya olmaydi.
- `pre_checkout_query`: payload egasi = to'lovchi, valyuta `XTR`, summa = joriy narx; aks holda to'lov rad etiladi.
- Payload tasodifiy nonce bilan; har `telegram_payment_charge_id` bir marta hisoblanadi (takroriy update'dan himoya).
- Faol obunada ikkinchi to'lov havolasi berilmaydi (ikki marta yechilmasin).
- `payments`/`subscriptions` akkaunt o'chirilganda ham saqlanadi (faqat ID, sana, summa — xabar matni yo'q).

## Bir nechta Telegram akkaunt (Pro)

Asosiy akkauntga yana **2 tagacha** Telegram akkaunt (ish, do'kon) ulanadi. Har biri o'z sessiyasi, sozlamasi,
qoidalari va qoralamalari bilan ishlaydi; obuna va boshqaruv — asosiy akkauntda.

Ulash (begona akkauntni "ulab olish"dan himoya — qo'shiladigan akkaunt egasi o'zi rozilik beradi):
1. Mini App → Hisob → Akkauntlar → «Akkaunt qo'shish»: nom va `t.me/<bot>?start=link_<token>` havola (30 daqiqa, bir martalik; bazada faqat xeshi).
2. Havolani **qo'shiladigan akkauntdan** ochish → bot rozilik matnini ko'rsatadi → «Roziman».
3. Mini App'da shu akkauntni tanlab Telegram'ga ulash (login faqat aynan shu akkaunt tasdiqlasa o'tadi).

- Mini App yuqorisida akkaunt almashtirgich; tanlangan akkaunt `X-Kotib-Account` sarlavhasi bilan yuboriladi va server uning asosiy akkauntga bog'langanini tekshiradi.
  Obuna, referal va akkauntlar ro'yxati har doim asosiy akkaunt bo'yicha.
- Qo'shimcha akkauntning bot xabarlari (qoralama kartalari, xulosa, eslatmalar) asosiy akkaunt chatiga «👤 nom» bilan keladi; tugmalar u yerdan ishlaydi.
  `/pause` va `/resume` barcha akkauntlarga, `/status` ularni ham ko'rsatadi.
- Qo'shimcha akkaunt faqat asosiysi Pro (yoki sinov) bo'lsa ishlaydi; sinovdagi kunlik limit umumiy.
  Kotib'da alohida ishlatilayotgan akkauntni qo'shib bo'lmaydi; qo'shimcha akkaunt Mini App'ni o'zi ocholmaydi.
- Uzish: Hisob → Akkauntlar → «Uzish» (sessiya tugatiladi, ma'lumotlari o'chadi). Asosiy akkaunt o'chirilsa, qo'shimchalari ham uziladi.

## Kengaytirilgan statistika (Pro)

Mini App → «📊 Batafsil statistika» (`GET /api/stats?days=7|30`): tayyorlangan va yuborilgan javoblar (o'tgan davrga
nisbatan o'zgarish bilan), tejalgan vaqt, qoralamalarni tasdiqlash ulushi, kunlik grafik, kontaktlar qaysi soatda
ko'p yozishi, ko'p yozgan kontaktlar, javobsiz qoldirilganlar. Hammasi audit hodisalaridan — xabar matnisiz
(`draft_created` va `auto_sent` detail'ida faqat suhbatdosh ID'si).

**Haftalik hisobot** — har dushanba 9:00 da (egasining vaqt zonasi) bot o'tgan hafta xulosasini yuboradi; Sozlamalarda o'chiriladi.

## Do'stni taklif qilish (referal)

To'lov yoqilganda (`PRICE_STARS`) har mijozning doimiy taklif havolasi bor: `https://t.me/<bot>?start=ref_<kod>`
(Mini App → **Hisob → Do'stni taklif qilish** yoki bot buyrug'i `/taklif`). Mukofot ikki tomonlama:

| Kim | Qachon | Nima oladi |
|---|---|---|
| Taklif qilingan do'st | havola orqali `/start` bosib, ro'yxatdan o'tsa | sinov **+7 kun** (`TRIAL_DAYS` + 7) |
| Taklif qiluvchi | do'st Telegram akkauntini **ulasa** | **+7 bonus kun** (ko'pi bilan 10 marta) |
| Taklif qiluvchi | do'st **birinchi marta to'lasa** | **+30 bonus kun** (cheklovsiz) |

- **Bonus kunlar** — to'langan obuna kabi to'liq xizmat (sinovdagi kunlik limit va qisqa kontekstsiz), Telegram pul yechmaydi.
  Bonus joriy sinov yoki to'langan muddat tugagach davom etadi. Avtomatik yangilanadigan obunada Telegram har 30 kunda
  pul yechadi, shuning uchun bonus har to'lovda keyingi davrdan keyinga suriladi — yo'qolmaydi, obuna bekor qilinsa ishlatiladi.
  To'lov qaytarilsa (refund), bonus darhol boshlanadi.
- **Firibgarlikdan himoya**: o'zini taklif qilib bo'lmaydi; faqat hali sinovi (obuna yozuvi) yo'q yangi odam taklif qilingan
  bo'la oladi; har odam bir marta; har bosqich mukofoti bir marta (atomar). Taklif yozuvlari akkaunt o'chirilganda ham
  saqlanadi — o'chirib qayta ochib mukofotni qayta olib bo'lmaydi. Ulanish mukofoti 10 ta bilan cheklangan (soxta
  akkauntlar bilan cheksiz bonus yig'ilmasin); to'lov mukofoti cheklanmagan — u haqiqiy daromad.
- **Eslatmalar**: sinov/bonus tugashidan 24 soat oldingi xabarda va Mini App bosh sahifasida (sinov ≤3 kun qolganda)
  do'st taklif qilish taklif etiladi. Mukofot berilganda taklif qiluvchiga bot xabar yuboradi.
- Admin `/stats` — taklif orqali kelganlar va ulardan to'laganlar soni.

**Blogerlar/kanallar uchun** kod shart emas: @BotFather → bot → **Affiliate Program** — Telegram'ning o'z hamkorlik
dasturi (Stars to'lovlaridan ulush foizi va muddatini belgilaysiz, hisob-kitobni Telegram yuritadi).

## Google integratsiyasi (Calendar, Contacts, Tasks)

Mijoz Mini App → **Hisob → Google'ni ulash** orqali o'z Google akkauntini ulaydi (tashqi brauzerda ochiladi:
Google Telegram ichidagi WebView'da kirishni bloklaydi). Shundan keyin kotib:

- **Taqvimni biladi**: keyingi 7 kundagi band vaqtlar (`freeBusy`, faqat vaqt — tadbir nomlari emas) modelga beriladi.
  Kontakt band vaqtga uchrashuv taklif qilsa, kotib muloyim boshqa vaqt so'raydi.
- **Kontaktni taniydi**: Google Contacts'dan telefon (bo'lmasa ism) bo'yicha ism va egasining eslatmasi ("Izoh") olinadi.
- **Uchrashuv/vazifani taklif qiladi**: suhbatda aniq vaqtli uchrashuv yoki egasiga topshiriq chiqsa, bot egasiga
  "📅 Aziz bilan uchrashuv, 15.10.2026 15:00–16:00. Google Calendar'ga qo'shaymi?" deb **tugmali xabar** yuboradi.
  Google'ga faqat **✅ Qo'shish** bosilganda yoziladi; avtomatik hech narsa qo'shilmaydi.

Xavfsizlik:
- OAuth: authorization code + PKCE, `state` bir martalik va 15 daqiqa amal qiladi, egasiga bog'langan.
- Refresh token AES-256-GCM bilan shifrlanadi (`aad = google:<owner>`), access token faqat xotirada.
- Tugma faqat egasida ishlaydi va amal holati atomar o'zgaradi — ikki marta bosish ikki tadbir yaratmaydi.
- Kontakt xabari asosida taklif qilingan nomda havola/raqam bo'lsa, taklif tashlanadi; egasiga takliflar
  5 ta, keyin ~10 daqiqada bittadan ko'p yuborilmaydi.
- "Uzish" va akkauntni o'chirish Google'da ham tokenni bekor qiladi (`revoke`). Mijoz ruxsatni Google'da olib tashlasa,
  ulanish avtomatik o'chiriladi va bot bir marta xabar beradi. Takliflar 7 kundan keyin o'chiriladi.

### Sozlash (Google Cloud Console)

1. Yangi loyiha → **APIs & Services → Library**: *Google Calendar API*, *People API*, *Google Tasks API*, *YouTube Data API v3* ni yoqing.
2. **OAuth consent screen** (Google Auth Platform): User type — *External*; ilova nomi, logo (`branding/logo-512.png`),
   **maxfiylik siyosati** va **foydalanish shartlari** havolalari, domen.
   Scopes: `calendar.events`, `calendar.freebusy`, `contacts.readonly`, `tasks`, YouTube plagini uchun `youtube.readonly` (hammasi *sensitive*, Gmail kabi *restricted* emas).
3. **Credentials → Create OAuth client ID → Web application**.
   Authorized redirect URI: `https://<domeningiz>/oauth/google/callback` (aynan `MINIAPP_URL` domeni).
4. `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` ni env'ga qo'ying va qayta ishga tushiring.

**Muhim — Google tekshiruvi (verification):**
- Ilova *Testing* holatida faqat qo'lda qo'shilgan **100 tagacha test foydalanuvchi** ulay oladi va ularning
  ulanishi **7 kunda tugaydi** (Google refresh tokenni bekor qiladi). Sinov uchun yetarli, sotuv uchun emas.
- *In production* uchun sensitive scope'lar bo'yicha Google tekshiruvidan o'tish kerak: tasdiqlangan domen,
  maxfiylik siyosati (Google ma'lumotlari qanday ishlatilishi, Gemini'ga yuborilishi), demo video. Odatda bir necha kundan
  bir necha haftagacha. Pullik xavfsizlik auditi (CASA) faqat restricted scope'lar (Gmail, Drive) uchun — bu yerda shart emas.
- Maxfiylik siyosatida ko'rsating: band vaqtlar va kontakt eslatmasi javob tayyorlash uchun Gemini'ga yuboriladi.

## Rejalashtirilgan vazifalar (Vazifalar bo'limi)

Claude'dagi *Scheduled* kabi: mijoz AI'ga ko'rsatma (prompt) va vaqt yozadi — kotib o'sha vaqtda
ko'rsatmani bajaradi va natijani bot chatiga «⏰ Nomi» bilan yuboradi.

- **Takrorlanish**: bir marta (sana + vaqt), har soat (daqiqasi), har kuni, ish kunlari (Du–Ju),
  haftaning tanlangan kunlari. Vaqt — egasining mintaqasida (Sozlamalar → Tinch soatlar), DST hisobga olinadi.
- Har vazifani yoqish/o'chirish, tahrirlash, o'chirish va **«Hozir bajarish»** mumkin; oxirgi natija va
  bajarilishlar soni ko'rinadi. Tayyor namunalar: ob-havo, valyuta kursi, kun yangiliklari, YouTube, motivatsiya.
- AI vazifani bajarishda **yoqilgan plaginlar** vositalaridan foydalanadi (Gemini function calling,
  ko'pi bilan 6 aylanish; vosita natijalari — faqat ma'lumot, ichidagi buyruqlar bajarilmaydi).
- Server o'chiq turgan paytda o'tib ketgan takrorlar yig'ilmaydi: vazifa bir marta bajariladi, keyingisi hozirdan.
  Har vazifa bazada atomar egallanadi (`ClaimSchedule`) — ikki marta bajarilmaydi.
- **Tarif**: Advanced va Pro (sinovda ham ochiq). Obuna tugasa vazifalar bajarilmaydi, yaratish/tahrirlash/«Hozir bajarish» yopiladi (402); o'chirish mumkin.
- Cheklovlar: egasida 20 ta vazifa, kuniga 60 ta bajarilish, ko'rsatma 2000 belgi.
- **Natijani kontaktga yuborish (Pro)**: «Natijani kimga yuborish» → «Kontaktga» — faqat egasining Telegram
  kontaktlaridan bittasi (Saved Messages va Telegram xizmati emas). Xabar egasining akkauntidan, uning nomidan ketadi;
  AI'ga «faqat xabar matnini, birinchi shaxsda yoz» ko'rsatmasi beriladi (`TaskRequest.Recipient`).
  Standart — avval egasiga **qoralama** (bot/Mini App'da bir bosishda yuboriladi). «Tasdiqsiz yuborish» yoqilsa ham
  quyidagilarda qoralama bo'ladi: javob filtri (havola, karta, parol...), qoida bu kontaktga yozishni o'chirgan,
  Telegram FloodWait, tezlik limiti yoki kuniga 20 tadan ortiq (`MaxTaskSendsPerDay`). Sessiya ulanmagan bo'lsa natija
  egasiga sabab bilan keladi. Har yuborilgan xabar auditga `schedule_sent` bo'lib yoziladi.

## Plaginlar (connector'lar)

**Faqat Pro tarifda** (sinovda ham ochiq). Advanced'da vazifalar plaginsiz bajariladi; plaginni yoqish 402 qaytaradi, o'chirib qo'yish va uzish har doim mumkin.

Vazifalar → «🧩 Plaginlar» (yoki Sozlamalar → Plaginlar). Har plagin AI'ga vosita beradi; mijoz uni
yoqadi va sozlaydi. API kalitlari bazada `crypto.Vault` bilan shifrlanadi (AAD — egasi + plagin) va
Mini App'ga qaytarilmaydi (faqat `••••1234`).

**YouTube — akkaunt orqali.** Oddiy foydalanuvchi API kalit nimaligini bilmaydi, shuning uchun YouTube
mavjud Google OAuth mijozi (`GOOGLE_CLIENT_ID/SECRET`) orqali ulanadi: alohida, faqat o'qish ruxsati
(`youtube.readonly`), alohida refresh token (taqvim ruxsatlariga qo'shilmaydi), xuddi shu redirect URI
(`/oauth/google/callback`). Token plagin sozlamasida shifrlangan; «Uzish» va akkauntni o'chirish uni Google'da
bekor qiladi. Google Cloud Console'da: **YouTube Data API v3**ni yoqing va OAuth consent screen'ga
`youtube.readonly` scope'ini qo'shing. Google sozlanmagan bo'lsa YouTube «tez orada» bo'lib turadi.
Kvota loyiha bo'yicha umumiy: qidiruv 100 birlik, obunalar ~1 birlik/kanal (standart 10 000/kun) — ko'p
foydalanuvchida Google'dan kvota oshirishni so'rang.

| Plagin | Vosita | Sozlama |
|---|---|---|
| 🌐 Internet qidiruvi | `web_search` — Gemini Google Search grounding, manbalari bilan | — |
| ▶️ YouTube | `youtube_search` — video qidirish; `youtube_subscriptions` — obuna kanallaridagi yangi videolar | **«YouTube akkauntini ulash»** tugmasi (Google OAuth, `youtube.readonly`) — API kalit kerak emas |
| 🌤 Ob-havo | `weather` — Open-Meteo (kalitsiz), 1–7 kun | asosiy shahar |
| 💱 Valyuta kursi | `exchange_rates` — O'zbekiston Markaziy banki | kuzatiladigan valyutalar |
| 📢 Telegram kanallar va guruhlar | `telegram_feed` — kotib ulangan sessiyadan kanal/guruhlardagi oxirgi xabarlar (arxivdagilarsiz, 30 tagacha chat, so'rovlar orasida 300 ms) | — (kalit kerak emas) |
| 🕌 Namoz vaqtlari | `prayer_times` — islomapi.uz (O'zbekiston), ishlamasa aladhan.com (Hanafiy, taxminiy) | shahar |
| 📰 Yangiliklar | `news_feed` — Kun.uz, Gazeta.uz, Daryo.uz, UzA, Spot.uz RSS (faqat shu ro'yxat — ixtiyoriy URL yo'q) | manbalar |
| 📊 Google Sheets | `sheets_read` — jadval qatorlari (faqat o'qish) | **«Google Sheets'ni ulash»** (OAuth, `spreadsheets.readonly`) + jadval havolasi (3 tagacha) |
| 💼 amoCRM / Kommo | `amocrm` — yangi lidlar, bajarilmagan vazifalar | akkaunt manzili + uzoq muddatli token |
| 🏢 Bitrix24 | `bitrix24` — lidlar, bitimlar, vazifalar | kiruvchi vebhuk (faqat `https://*.bitrix24.*/rest/<id>/<kalit>/` — SSRF himoyasi) |
| Tez orada | Gmail, Instagram, Notion, Google Drive, sport natijalari | — |

Google Sheets uchun Google Cloud Console'da **Google Sheets API**ni yoqing va consent screen'ga
`spreadsheets.readonly` scope'ini qo'shing (sensitive, restricted emas). Telegram plagini guruh xabarlarini ham
xulosa uchun AI'ga yuboradi — mijozga Mini App'da shu haqida yozilgan.

Yangi plagin qo'shish: `internal/plugin` da `Plugin` interfeysini (Info, Tools, Call) amalga oshirib,
`plugin.Default` ro'yxatiga qo'shing — Mini App katalogi va sozlash formasi `Info.Fields` dan avtomatik chiziladi.

## Telegram akkauntini ulash

Faqat **telefon raqam va kod** orqali (Mini App → ulash oynasi): Telegram kodni SMS yoki Telegram
ilovasiga yuboradi, kod qayerga yuborilgani (Telegram chati, SMS, qo'ng'iroq, email) ko'rsatiladi.
2FA paroli bo'lsa, alohida so'raladi (saqlanmaydi).

Har bosqichda **«Orqaga»** bor (Mini App tugmasi va Telegram BackButton): jarayondagi ulanish
bekor qilinadi (`POST /api/login/cancel`) va raqam kiritish sahifasiga qaytiladi — kod kelmasa yoki
raqam xato bo'lsa ilova qotib qolmaydi.

Kirilgan akkaunt **Mini App'ni ochgan foydalanuvchining o'zi** bo'lishi shart (`self.id == owner`):
boshqa odamning raqami kiritilsa, sessiya darhol uziladi.

## Telefon qulayliklari (iPhone va Android)

| Qulaylik | Qayerda yoqiladi | Izoh |
|---|---|---|
| **Qoralama bot chatida** | Sozlamalar → "Qoralamalar bot chatida" | Bot qoralamani **[📤 Yuborish] [✖️ Rad etish]** bilan yuboradi. Kartaga javob qilib matn yozsangiz — shu matn ketadi, "ok" — asl qoralama. Android'da bildirishnomadan javob berish mumkin. Default o'chiq: matnlar bot chati tarixida qoladi |
| **Taqvimga taklif** | Sozlamalar → "Uchrashuvlarni taqvimga taklif qilish" (default yoqiq) | Google ulangan bo'lsa — Google Calendar/Tasks. Ulanmagan bo'lsa — ✅ bosilganda imzolangan `.ics` havola: iPhone'da Apple Taqvim, Android'da taqvim ilovasi o'zi "qo'shish"ni taklif qiladi (15 daqiqa oldin eslatma bilan, havola 7 kun amal qiladi) |
| **Ovozli xabarlar** | Sozlamalar → "Ovozli xabarlarga ham" (default o'chiq) | 2 daqiqagacha ovoz Gemini'ga audio sifatida yuboriladi. Audio matn filtridan o'tolmagani uchun javob **har doim faqat qoralama** |
| **Avtomatlashtirish** | Hisob → Avtomatlashtirish | Shaxsiy maxfiy havola: iPhone Shortcuts (Focus: Haydash, Uyqu), Android MacroDroid/Tasker (Bezovta qilmang, mashina Bluetooth'i) kotib rejimini va "hozirgi holat"ini o'zgartiradi. Token faqat xeshi bilan saqlanadi |
| **Hozirgi holat** | Bosh sahifa → "Hozir nima qilyapsiz?" (yoki Sozlamalar) | 15 ta tayyor holat: rulda, yig'ilishda, uxlayapti, dam olmoqda, sayrda, musiqa tinglamoqda, dars qilmoqda, dastur tuzmoqda, ishda band, sportda, namozda, ovqatlanmoqda, oila bilan, safarda, betob — yoki o'z matningiz. Bosh sahifada muddat bilan (30 daqiqa – 4 soat yoki muddatsiz): vaqt tugagach holat o'zi o'chadi. Kotib javobda hisobga oladi (aniq vaqt va'da qilmaydi) |
| **Siri / Google Assistant** | — | `t.me/<bot>?start=pause` va `?start=resume` deep link'lari |
| **Face ID / barmoq izi** | Hisob → Telefon | Mini App'dan yuborish, avto rejimni yoqish, Google'ni uzish, akkauntni o'chirishdan oldin (shu qurilmada) |
| **Bosh ekranga qo'shish** | Hisob → Telefon | Telegram `addToHomeScreen` (Telegram 8.0+). Eski ilovada yoki qo'llamaydigan qurilmada qo'lda qo'shish yo'riqnomasi ko'rsatiladi: iPhone — Safari'da `t.me/<bot>?startapp` → «Bosh ekranga»; Android — bot chati ⋮ menyusi. `?startapp` ishlashi uchun @BotFather → Bot Settings → **Configure Mini App** (Main Mini App) yoqilgan bo'lishi kerak |
| **Orqaga tugmasi** | — | Telegram BackButton (Android tizim tugmasi ham): ichki sahifadan yoki bo'limdan Asosiy'ga; ulanish jarayonida — raqam kiritishga |
| **Pastga surib yopilmaslik** | — | `disableVerticalSwipes`: matn yozayotganda ilova tasodifan yopilmaydi |

Avtomatlashtirish havolasi parametrlari: `mode=draft|auto|off`, `away=<matn>` (bo'sh — tozalash), `for=<daqiqa>` (holat shuncha vaqtdan keyin o'zi tugaydi, 1440 gacha), `action=pause|resume`.
Masalan: `https://<domen>/hook/<token>?mode=auto&away=Hozir%20rulda`. GET ham, POST ham ishlaydi.

## Admin paneli (`ADMIN_IDS`)

`ADMIN_IDS` dagi foydalanuvchilar Mini App'da (Asosiy → «👑 Admin paneli», Hisob → «👑 Admin obunasi») panelni ko'radi;
boshqalarga `/api/admin/*` 404 qaytaradi. Xabar matnlari ko'rsatilmaydi — faqat audit hodisalari soni va sozlamalar.

| Bo'lim | Nima bor |
|---|---|
| **Umumiy** | foydalanuvchilar, faol, yangi va ilovani ochganlar (7 kun), to'lovlar/tushum/referal, xavfsizlik darajasi, ish rejimlari, foydalanish belgisi, holatlar, 7 kunlik hodisalar. Qatorni bosish — tegishli filtr |
| **Foydalanuvchilar** | qidiruv (ism, @username, ID) va filtrlar: faol, avtomatik, xavfli, to'laganlar, nofaol, ulanmagan, bloklangan |
| **Foydalanuvchi** | ma'lumot, obuna, sozlamalar xulosasi, 7/30 kunlik hodisalar, oxirgi hodisalar; amallar: 🎁 sovg'a obuna (7 kun – muddatsiz, Advanced/Pro) va uni bekor qilish, 📩 xabar, ⏸ to'xtatish, ⛔ bloklash / blokni olish |
| **To'lovlar** | oxirgi 200 to'lov, «Qaytarish» (refund) |
| **Xabar** | rozilik bergan va bloklanmaganlarga ommaviy xabar (fonda, ~20 xabar/s) |

**Baholash** (taxminiy, admin e'tibori uchun — avtomatik jazo yo'q):
- *Xavfsizlik darajasi* sozlamalardan: avtomatik rejim yoki avto qoidalar — o'rta; ular bilan birga notanishlarga/botlarga/guruhlarga javob yoki havolalar — yuqori.
- *Foydalanish* oxirgi 7 kun hodisalaridan: Telegram flood cheklovi, favqulodda to'xtash, ≥10 filtr rad etgan javob yoki ≥700 avtomatik xabar — **shubhali**; ≥3 rad etilgan, ≥10 xavfli kiruvchi, ≥300 avtomatik xabar yoki sessiya xatosi — **kuzatish kerak**; hech narsa tayyorlanmagan — nofaol.

**Bloklash**: `bans` jadvali (akkauntga FK yo'q — akkauntni o'chirib qayta ochish blokni olmaydi). Kotib darhol to'xtaydi (holat `paused`),
avtomatik to'lov bekor qilinadi, `Entitled`/`HasPro` false (vazifalar, xulosalar to'xtaydi), Telegram'ni qayta ulash va `/resume` rad etiladi,
Mini App'da faqat `GET /api/me` va akkauntni o'chirish ochiq, bot buyruqlari va to'lov (`pre_checkout`) rad etiladi. Adminni bloklab bo'lmaydi.
**Sovg'a obuna** — `BonusUntil` (referal bonusi kabi): joriy sinov/to'langan muddatdan keyin qo'shiladi, Pro sovg'asi tarifni Pro qiladi.
**Admin obunasi** — `ADMIN_IDS` uchun doimiy Pro, limitsiz va bepul (sotib olinmaydi). Har admin amali foydalanuvchi auditiga `admin=<id>` bilan va server logiga yoziladi.

## Bot buyruqlari

| Buyruq | Kim | Vazifa |
|---|---|---|
| `/start`, `/help` | hamma | Mini App'ni ochish, yordam (`/start pause`, `/start resume`, `/start ref_<kod>` — deep link) |
| `/status` | hamma | holat, kutilayotgan qoralamalar, obuna |
| `/pause`, `/resume` | hamma | kotibni darhol to'xtatish / davom ettirish |
| `/obuna` | hamma | tariflar va to'lov havolalari (Advanced / Pro), tarifni almashtirish |
| `/taklif` | hamma | do'stni taklif qilish havolasi va natijalar (bonus kunlar) |
| `/sor <savol>` | Pro | yozishma haqida savol: «/sor Aziz bilan nima kelishdik?» |
| `/stats` | `ADMIN_IDS` | akkauntlar, faol obunalar, tushum |
| `/refund <charge_id>` | `ADMIN_IDS` | to'lovni qaytarish |

## Xavfsizlik dizayni

- **Session** AES-256-GCM bilan shifrlanadi (har yozuv uchun alohida DEK, KEK env/KMS'da), `aad = session:<owner>` — blobni boshqa qatorga ko'chirib bo'lmaydi.
- **2FA paroli** va login kodi saqlanmaydi/log qilinmaydi.
- **Mini App auth**: `initData` HMAC (Telegram algoritmi), 24 soat muddat, har foydalanuvchiga API tezlik cheklovi, barcha so'rovlar egasi bo'yicha ajratilgan.
- **Login himoyasi**: bir soatda ko'pi bilan 5 ta login urinishi; kod/parol faqat rozilik berilgandan keyin qabul qilinadi; akkaunt o'chirilsa jarayondagi login bekor qilinadi.
- **HTTP**: qat'iy CSP (`unsafe-inline`siz), HSTS, COOP/CORP, Permissions-Policy; statik serverda papka ro'yxati va yashirin fayllar (`.env`, `.git`) berilmaydi; JSON'da noma'lum maydonlar rad etiladi; yaroqsiz `initData` oqimi umumiy limit bilan cheklanadi (429).
- **Mini App**: hech qayerda `innerHTML` yo'q, inline skript/stil yo'q; tugmalar bosilganda bloklanadi (ikki marta yuborish/to'lovdan himoya).
- **Default = draft rejimi.** Avto-rejimda ham: sezgir mavzu (pul, parol/kod, hujjat, "bot misan?"...) → faqat draft; prompt-injection belgisi → modelga umuman yuborilmaydi.
- **Kontaktda yo'q** odamlar, guruh, kanal, botlar — default javobsiz. Birinchi bo'lib yozish kodda yo'q.
- **Botlar**: sozlamada "Botlarga ham" yoqilsa (default o'chiq) — faqat qoralama, hech qachon avtomatik emas (bot ↔ kotib cheksiz yozishmasi = spam/ban). Telegram xizmati (777000, login kodlari) har qanday sozlamada e'tiborsiz qoldiriladi va AI'ga yuborilmaydi.
- **Ban xavfini kamaytirish**: token-bucket (global/chat/kunlik), o'qish+yozish kechikishi, "typing", xabarlarni birlashtirish (debounce), FloodWait'ga hurmat, `PEER_FLOOD`/revoked → circuit breaker (avtomatik qayta yoqilmaydi), barqaror qurilma ma'lumoti, sessiyalarni 2s oraliqda ulash.
- **Model chiqishi** tekshiriladi: havola/@username/karta yoki telefon raqami/AI-sizib chiqishi/uzunlik → draft.
- **Maxfiylik**: audit logda xabar matni yo'q; ilova har soatda 7 kundan eski draftlar va o'qitish juftliklarini matni bilan tozalaydi, 30 kun faolligi yo'q chat metalarini va 90 kundan eski auditni o'chiradi; "Akkauntni o'chirish" → `auth.logOut` + CASCADE.
  AI xulosa, uslub o'rganish va `/sor` uchun yozishmalar shu payt Telegram'dan o'qiladi va bazaga yozilmaydi; ularning ichidagi buyruqlar modelga «faqat ma'lumot» sifatida o'ralgan holda beriladi.
- **Ishonchlilik**: yuborilmay qolgan draft yo'qolmaydi (qayta "pending"ga qaytadi); Telegram fatal xatosida circuit breaker deadlock'siz ishlaydi; xotira cheksiz o'smasligi uchun barcha keshlar chegaralangan.

## Bilishingiz shart bo'lgan cheklovlar

1. **Telegram userbotlarga qarshi.** Ban xavfi kamayadi, lekin **yo'qolmaydi**. Mijozlarga buni oldindan, ochiq ayting.
2. **Bitta `api_id`** barcha mijozlar uchun — Telegram uni cheklasa, hamma sessiyalar ta'sirlanadi. Bu biznes uchun asosiy xavf.
3. Login kodini Telegram boshqa qurilmaga kiritilganini sezib, kirishni bloklashi mumkin ("login attempt blocked"). Mijozga kodni chatda yozmaslikni ayting.
4. Mijoz nomidan javob berish — mijozning suhbatdoshlari uchun shaffoflik masalasi. Model "inson ekanini" tasdiqlamaydi, lekin ishlatish qoidalarida (ToS) mijoz javobgarligini aniq yozing; ro'yxatdan o'tish paytidagi rozilik (`consent_at`) saqlanadi.
5. Qonuniy tomon: mijozning suhbatdoshlari xabarlari Gemini'ga (Google) yuboriladi — maxfiylik siyosatida ko'rsating; O'zbekiston "Shaxsga doir ma'lumotlar to'g'risida"gi qonuni talablarini yurist bilan tekshiring.
6. Faqat **shaxsiy matnli, ovozli va rasmli xabarlar** (ovoz va rasm — faqat qoralama): guruh, video, hujjat hozircha qo'llab-quvvatlanmaydi. `updates.Manager` qisqa uzilishlarni tiklaydi, lekin holat xotirada saqlanadi: ilova qayta ishga tushganda o'chiq paytdagi xabarlar javobsiz qoladi.

## Keyingi qadamlar

- `gotd` bilan real sinov (o'zingizning akkaunt); `updates.Manager` holatini Postgres'da saqlash.
- `MASTER_KEY` rotatsiyasi uchun env/KMS'dan bir nechta kalit yuklash (`crypto.StaticKeys` buni qo'llaydi).
- Model-asosli sezgir mavzu klassifikatori (regex birinchi qatlam, to'liq emas).
- Admin panel (veb), monitoring (Prometheus).
