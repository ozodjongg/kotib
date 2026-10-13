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
webapp/               Mini App (vanilla JS, innerHTML ishlatilmaydi)
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
| `LISTEN` | aniq manzil (masalan `127.0.0.1:8080`); berilmasa `PORT` ishlatiladi |
| `PORT` | platforma beradi (Railway); `0.0.0.0:$PORT` da tinglanadi. Ikkalasi ham bo'lmasa — `127.0.0.1:8080` |
| `STATIC_DIR` | Mini App fayllari, default `./webapp` |
| `DEV_MEMORY=1` | faqat dev: xotirada saqlash |

## Xavfsizlik dizayni

- **Session** AES-256-GCM bilan shifrlanadi (har yozuv uchun alohida DEK, KEK env/KMS'da), `aad = session:<owner>` — blobni boshqa qatorga ko'chirib bo'lmaydi.
- **2FA paroli** va login kodi saqlanmaydi/log qilinmaydi.
- **Mini App auth**: `initData` HMAC (Telegram algoritmi), 24 soat muddat, har foydalanuvchiga API tezlik cheklovi, barcha so'rovlar egasi bo'yicha ajratilgan.
- **Login himoyasi**: bir soatda ko'pi bilan 5 ta login urinishi; kod/parol faqat rozilik berilgandan keyin qabul qilinadi; akkaunt o'chirilsa jarayondagi login bekor qilinadi.
- **HTTP**: qat'iy CSP (`unsafe-inline`siz), HSTS, COOP/CORP, Permissions-Policy; statik serverda papka ro'yxati va yashirin fayllar (`.env`, `.git`) berilmaydi; JSON'da noma'lum maydonlar rad etiladi.
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
- Mijoz uchun to'lov/obuna, admin panel, monitoring (Prometheus).
