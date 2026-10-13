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
(login holati ham xotirada). Railway IP'lari umumiy — Telegram login'ni bloklasa, `TG_PROXY` qo'shing.

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
| `ADMIN_IDS` | admin Telegram ID'lari, vergul bilan: `/stats`, `/refund`; adminlar uchun kotib bepul |
| `LISTEN` | aniq manzil (masalan `127.0.0.1:8080`); berilmasa `PORT` ishlatiladi |
| `PORT` | platforma beradi (Railway); `0.0.0.0:$PORT` da tinglanadi. Ikkalasi ham bo'lmasa — `127.0.0.1:8080` |
| `STATIC_DIR` | Mini App fayllari, default `./webapp` |
| `DEV_MEMORY=1` | faqat dev: xotirada saqlash |

## To'lov: Telegram Stars obunasi

`PRICE_STARS` berilsa, to'lov yoqiladi:

- Rozilik berilganda **bepul sinov** (`TRIAL_DAYS`) boshlanadi. Sinov bir marta beriladi: akkauntni o'chirib qayta ochish yangi sinov bermaydi.
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

## Bot buyruqlari

| Buyruq | Kim | Vazifa |
|---|---|---|
| `/start`, `/help` | hamma | Mini App'ni ochish, yordam |
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
6. Faqat **shaxsiy matnli xabarlar**: guruh, ovoz, rasm hozircha qo'llab-quvvatlanmaydi. `updates.Manager` qisqa uzilishlarni tiklaydi, lekin holat xotirada saqlanadi: ilova qayta ishga tushganda o'chiq paytdagi xabarlar javobsiz qoladi.

## Keyingi qadamlar

- `gotd` bilan real sinov (o'zingizning akkaunt); `updates.Manager` holatini Postgres'da saqlash.
- `MASTER_KEY` rotatsiyasi uchun env/KMS'dan bir nechta kalit yuklash (`crypto.StaticKeys` buni qo'llaydi).
- Model-asosli sezgir mavzu klassifikatori (regex birinchi qatlam, to'liq emas).
- Admin panel (veb), monitoring (Prometheus).
