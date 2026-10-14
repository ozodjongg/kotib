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
internal/llm/         Gemini REST klienti + system prompt
internal/userbot/     pipeline (Telegram'dan mustaqil), manager, login, gotd.go (build tag)
internal/miniapp/     initData HMAC auth + HTTP API
internal/store/       Store interfeysi, Postgres, Memory (dev)
internal/bot/         boshqaruv boti (/start, bildirishnoma)
internal/billing/     Telegram Stars obunasi: sinov, to'lov tasdig'i, bekor qilish, refund, eslatmalar
internal/google/      Google: OAuth (PKCE), Calendar (band vaqt, tadbir), Contacts (eslatma), Tasks
internal/ics/         Google'siz taqvim: .ics fayl va imzolangan havola
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
| `PRICE_STARS` | oylik obuna narxi (Telegram Stars, 1–10000). Bo'sh/0 — to'lov o'chiq, xizmat bepul |
| `TRIAL_DAYS` | bepul sinov kunlari (default 7) |
| `TRIAL_DAILY_LIMIT` | sinovda kuniga ko'pi bilan nechta javob tayyorlanadi (default 50; 0 — cheksiz). To'langan obunada cheklov yo'q |
| `ADMIN_IDS` | admin Telegram ID'lari, vergul bilan: `/stats`, `/refund`; adminlar uchun kotib bepul |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | ixtiyoriy: Google integratsiyasi (pastda). Bo'sh — o'chiq |
| `GOOGLE_REDIRECT_URL` | ixtiyoriy; default `<MINIAPP_URL domeni>/oauth/google/callback` |
| `LISTEN` | aniq manzil (masalan `127.0.0.1:8080`); berilmasa `PORT` ishlatiladi |
| `PORT` | platforma beradi (Railway); `0.0.0.0:$PORT` da tinglanadi. Ikkalasi ham bo'lmasa — `127.0.0.1:8080` |
| `STATIC_DIR` | Mini App fayllari, default `./webapp` |
| `DEV_MEMORY=1` | faqat dev: xotirada saqlash |

## To'lov: Telegram Stars obunasi

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

1. Yangi loyiha → **APIs & Services → Library**: *Google Calendar API*, *People API*, *Google Tasks API* ni yoqing.
2. **OAuth consent screen** (Google Auth Platform): User type — *External*; ilova nomi, logo (`branding/logo-512.png`),
   **maxfiylik siyosati** va **foydalanish shartlari** havolalari, domen.
   Scopes: `calendar.events`, `calendar.freebusy`, `contacts.readonly`, `tasks` (hammasi *sensitive*, Gmail kabi *restricted* emas).
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

## Telegram akkauntini ulash

Ikki yo'l bor (Mini App → ulash oynasi):

1. **Kodsiz (tavsiya etiladi)** — Telegram Desktop'dagi QR-kirish kabi: server login token oladi
   (`auth.exportLoginToken`), mijoz «Telegram'da tasdiqlash» tugmasini bosadi va Telegram ilovasida
   kirishni tasdiqlaydi (`tg://login?token=…`). Tugma ishlamasa — boshqa qurilmadan QR skanerlash
   (Sozlamalar → Qurilmalar → Kompyuterni ulash). Token ~30 soniyada yangilanadi. SMS/kod yetkazilmasa
   ham ishlaydi; 2FA paroli bo'lsa, alohida so'raladi.
2. **Telefon raqam va kod** — kod qayerga yuborilgani (Telegram chati, SMS, qo'ng'iroq, email) ko'rsatiladi.

Ikkala yo'lda ham tasdiqlangan akkaunt **Mini App'ni ochgan foydalanuvchining o'zi** bo'lishi shart
(`self.id == owner`): havolani boshqa odam tasdiqlasa, sessiya darhol uziladi.

## Telefon qulayliklari (iPhone va Android)

| Qulaylik | Qayerda yoqiladi | Izoh |
|---|---|---|
| **Qoralama bot chatida** | Sozlamalar → "Qoralamalar bot chatida" | Bot qoralamani **[📤 Yuborish] [✖️ Rad etish]** bilan yuboradi. Kartaga javob qilib matn yozsangiz — shu matn ketadi, "ok" — asl qoralama. Android'da bildirishnomadan javob berish mumkin. Default o'chiq: matnlar bot chati tarixida qoladi |
| **Taqvimga taklif** | Sozlamalar → "Uchrashuvlarni taqvimga taklif qilish" (default yoqiq) | Google ulangan bo'lsa — Google Calendar/Tasks. Ulanmagan bo'lsa — ✅ bosilganda imzolangan `.ics` havola: iPhone'da Apple Taqvim, Android'da taqvim ilovasi o'zi "qo'shish"ni taklif qiladi (15 daqiqa oldin eslatma bilan, havola 7 kun amal qiladi) |
| **Ovozli xabarlar** | Sozlamalar → "Ovozli xabarlarga ham" (default o'chiq) | 2 daqiqagacha ovoz Gemini'ga audio sifatida yuboriladi. Audio matn filtridan o'tolmagani uchun javob **har doim faqat qoralama** |
| **Avtomatlashtirish** | Hisob → Avtomatlashtirish | Shaxsiy maxfiy havola: iPhone Shortcuts (Focus: Haydash, Uyqu), Android MacroDroid/Tasker (Bezovta qilmang, mashina Bluetooth'i) kotib rejimini va "hozirgi holat"ini o'zgartiradi. Token faqat xeshi bilan saqlanadi |
| **Hozirgi holat** | Sozlamalar → "Hozirgi holatingiz" | "Hozir rulda" — kotib javobda hisobga oladi (aniq vaqt va'da qilmaydi) |
| **Siri / Google Assistant** | — | `t.me/<bot>?start=pause` va `?start=resume` deep link'lari |
| **Face ID / barmoq izi** | Hisob → Telefon | Mini App'dan yuborish, avto rejimni yoqish, Google'ni uzish, akkauntni o'chirishdan oldin (shu qurilmada) |
| **Bosh ekranga qo'shish** | Hisob → Telefon | Telegram `addToHomeScreen` (Telegram 8.0+). Eski ilovada yoki qo'llamaydigan qurilmada qo'lda qo'shish yo'riqnomasi ko'rsatiladi: iPhone — Safari'da `t.me/<bot>?startapp` → «Bosh ekranga»; Android — bot chati ⋮ menyusi. `?startapp` ishlashi uchun @BotFather → Bot Settings → **Configure Mini App** (Main Mini App) yoqilgan bo'lishi kerak |
| **Orqaga tugmasi** | — | Telegram BackButton (Android tizim tugmasi ham): bo'limdan Asosiy'ga |
| **Pastga surib yopilmaslik** | — | `disableVerticalSwipes`: matn yozayotganda ilova tasodifan yopilmaydi |

Avtomatlashtirish havolasi parametrlari: `mode=draft|auto|off`, `away=<matn>` (bo'sh — tozalash), `action=pause|resume`.
Masalan: `https://<domen>/hook/<token>?mode=auto&away=Hozir%20rulda`. GET ham, POST ham ishlaydi.

## Bot buyruqlari

| Buyruq | Kim | Vazifa |
|---|---|---|
| `/start`, `/help` | hamma | Mini App'ni ochish, yordam (`/start pause`, `/start resume` — deep link) |
| `/status` | hamma | holat, kutilayotgan qoralamalar, obuna |
| `/pause`, `/resume` | hamma | kotibni darhol to'xtatish / davom ettirish |
| `/obuna` | hamma | Stars bilan obuna bo'lish |
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
- **Maxfiylik**: audit logda xabar matni yo'q; ilova har soatda 7 kundan eski draftlarni matni bilan tozalaydi, 90 kundan eski auditni o'chiradi; "Akkauntni o'chirish" → `auth.logOut` + CASCADE.
- **Ishonchlilik**: yuborilmay qolgan draft yo'qolmaydi (qayta "pending"ga qaytadi); Telegram fatal xatosida circuit breaker deadlock'siz ishlaydi; xotira cheksiz o'smasligi uchun barcha keshlar chegaralangan.

## Bilishingiz shart bo'lgan cheklovlar

1. **Telegram userbotlarga qarshi.** Ban xavfi kamayadi, lekin **yo'qolmaydi**. Mijozlarga buni oldindan, ochiq ayting.
2. **Bitta `api_id`** barcha mijozlar uchun — Telegram uni cheklasa, hamma sessiyalar ta'sirlanadi. Bu biznes uchun asosiy xavf.
3. Login kodini Telegram boshqa qurilmaga kiritilganini sezib, kirishni bloklashi mumkin ("login attempt blocked"). Mijozga kodni chatda yozmaslikni ayting.
4. Mijoz nomidan javob berish — mijozning suhbatdoshlari uchun shaffoflik masalasi. Model "inson ekanini" tasdiqlamaydi, lekin ishlatish qoidalarida (ToS) mijoz javobgarligini aniq yozing; ro'yxatdan o'tish paytidagi rozilik (`consent_at`) saqlanadi.
5. Qonuniy tomon: mijozning suhbatdoshlari xabarlari Gemini'ga (Google) yuboriladi — maxfiylik siyosatida ko'rsating; O'zbekiston "Shaxsga doir ma'lumotlar to'g'risida"gi qonuni talablarini yurist bilan tekshiring.
6. Faqat **shaxsiy matnli va ovozli xabarlar** (ovoz — faqat qoralama): guruh, rasm, video hozircha qo'llab-quvvatlanmaydi. `updates.Manager` qisqa uzilishlarni tiklaydi, lekin holat xotirada saqlanadi: ilova qayta ishga tushganda o'chiq paytdagi xabarlar javobsiz qoladi.

## Keyingi qadamlar

- `gotd` bilan real sinov (o'zingizning akkaunt); `updates.Manager` holatini Postgres'da saqlash.
- `MASTER_KEY` rotatsiyasi uchun env/KMS'dan bir nechta kalit yuklash (`crypto.StaticKeys` buni qo'llaydi).
- Model-asosli sezgir mavzu klassifikatori (regex birinchi qatlam, to'liq emas).
- Admin panel (veb), monitoring (Prometheus).
