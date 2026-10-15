package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
	"github.com/ozodjongg/secretar_ai_agent/internal/bot"
	"github.com/ozodjongg/secretar_ai_agent/internal/botcmd"
	"github.com/ozodjongg/secretar_ai_agent/internal/crypto"
	"github.com/ozodjongg/secretar_ai_agent/internal/google"
	"github.com/ozodjongg/secretar_ai_agent/internal/ics"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
	"github.com/ozodjongg/secretar_ai_agent/internal/userbot"
	"github.com/ozodjongg/secretar_ai_agent/migrations"
)

const (
	draftTTL = 7 * 24 * time.Hour  // draftlar (kontakt xabari matni bilan) shundan ko'p saqlanmaydi
	auditTTL = 90 * 24 * time.Hour // audit (matnsiz meta) saqlash muddati
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("to'xtadi", "err", err)
		os.Exit(1)
	}
}

func env(key string) string { return os.Getenv(key) }

func must(keys ...string) error {
	for _, k := range keys {
		if env(k) == "" {
			return fmt.Errorf("muhit o'zgaruvchisi kerak: %s", k)
		}
	}
	return nil
}

func run(log *slog.Logger) error {
	if err := must("BOT_TOKEN", "MINIAPP_URL", "GEMINI_API_KEY", "GEMINI_MODEL", "MASTER_KEY", "TG_APP_ID", "TG_APP_HASH"); err != nil {
		return err
	}
	appID, err := strconv.Atoi(env("TG_APP_ID"))
	if err != nil || appID <= 0 {
		return errors.New("TG_APP_ID musbat raqam bo'lishi kerak")
	}
	if u, err := url.Parse(env("MINIAPP_URL")); err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("MINIAPP_URL https:// bilan boshlanadigan to'liq manzil bo'lishi kerak")
	}

	price, err := intEnv("PRICE_STARS", 0, 0, billing.MaxPrice)
	if err != nil {
		return err
	}
	// Ikkinchi tarif (Pro). Bo'sh/0 — bitta tarif: obunada hamma funksiya ochiq.
	proPrice, err := intEnv("PRICE_PRO_STARS", 0, 0, billing.MaxPrice)
	if err != nil {
		return err
	}
	if proPrice > 0 && proPrice <= price {
		return errors.New("PRICE_PRO_STARS PRICE_STARS'dan (Advanced) katta bo'lishi kerak")
	}
	trialDays, err := intEnv("TRIAL_DAYS", 7, 0, 90)
	if err != nil {
		return err
	}
	trialDaily, err := intEnv("TRIAL_DAILY_LIMIT", 50, 0, 100000)
	if err != nil {
		return err
	}
	admins, err := parseIDs(env("ADMIN_IDS"))
	if err != nil {
		return err
	}

	keys, err := crypto.NewStaticKeyFromBase64(env("MASTER_KEY"))
	if err != nil {
		return err
	}
	vault := crypto.NewVault(keys)

	// ---- Saqlash ----
	var st store.Store
	var pgDB *sql.DB // bitta nusxa qulfi uchun (faqat Postgres)
	switch {
	case env("DATABASE_URL") != "":
		db, err := sql.Open("pgx", env("DATABASE_URL"))
		if err != nil {
			return fmt.Errorf("baza: %w", err)
		}
		defer db.Close()
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(30 * time.Minute)
		db.SetConnMaxIdleTime(5 * time.Minute)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = db.PingContext(ctx)
		if err == nil {
			err = migrations.Apply(ctx, db)
		}
		cancel()
		if err != nil {
			// Xato matnida DATABASE_URL (parol) bo'lmasligi uchun faqat qisqa sabab.
			return fmt.Errorf("baza: %w", err)
		}
		st = store.NewPostgres(db)
		pgDB = db
	case env("DEV_MEMORY") == "1":
		log.Warn("XOTIRADAGI saqlash: qayta ishga tushganda barcha sessiyalar yo'qoladi (faqat dev)")
		st = store.NewMemory()
	default:
		return errors.New("DATABASE_URL kerak (yoki faqat dev uchun DEV_MEMORY=1)")
	}

	// ---- Qismlar ----
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctrlBot := bot.New(env("BOT_TOKEN"), env("MINIAPP_URL"), log)
	// Qo'shimcha (bog'langan) akkauntlarning bot xabarlari asosiy akkaunt chatiga «👤 nom» bilan boradi.
	notify := func(ctx context.Context, owner int64, text string, openApp bool) {
		chat, text := botcmd.Route(ctx, st, owner, text)
		if err := ctrlBot.Notify(ctx, chat, text, openApp); err != nil {
			log.Warn("bildirishnoma yuborilmadi", "owner", owner, "err", err)
		}
	}

	conn, err := userbot.NewGotdConnector(userbot.GotdConfig{
		AppID: appID, AppHash: env("TG_APP_HASH"),
		Store: st, Vault: vault, Proxy: env("TG_PROXY"), Log: log,
	})
	if err != nil {
		return err
	}

	bill := &billing.Service{Store: st, API: ctrlBot, Price: price, ProPrice: proPrice, TrialDays: trialDays, TrialDailyLimit: trialDaily, Exempt: admins, Log: log,
		Notify: notify}
	if bill.Enabled() {
		log.Info("to'lov yoqilgan (Telegram Stars)", "price", price, "pro_price", proPrice, "trial_days", trialDays, "trial_daily_limit", trialDaily)
	}

	mgr := &userbot.Manager{
		Store: st, Conn: conn, Log: log, Notify: notify,
		LLM:      llm.NewGemini(env("GEMINI_API_KEY"), env("GEMINI_MODEL")),
		Entitled: bill.Entitled,
		Quota:    bill.UseQuota,
		// Kontekst: obunada oxirgi 20 ta, sinovda 8 ta xabar.
		HistoryLimit: bill.HistoryLimit,
		// Taklif qilingan do'st akkauntini ulasa — taklif qiluvchiga bonus kunlar.
		OnConnected: bill.ReferralActivated,
		// Ovozli xabarlar va Google — Pro tarif (sinovda ham ochiq).
		Pro: bill.HasPro,
	}
	// Tasdiq tugmali takliflar (uchrashuv/vazifa) — Google bo'lsa ham, bo'lmasa ham (.ics).
	mgr.Propose = func(ctx context.Context, owner, id int64, text string) {
		chat, text := botcmd.Route(ctx, st, owner, text)
		if err := ctrlBot.Ask(ctx, chat, text, botcmd.ActionButtons(id)); err != nil {
			log.Warn("amal taklifi yuborilmadi", "owner", owner, "err", err)
		}
	}
	mgr.CalendarFallback = true
	icsKey := sha256.Sum256([]byte("kotib-ics:" + env("MASTER_KEY")))
	icsSigner := &ics.Signer{Key: icsKey[:], Base: publicURL(env("MINIAPP_URL"))}

	// Faqat bitta nusxa Telegram'ga ulansin: deploy paytida yangi nusxa eskisi to'xtashini kutadi.
	gate := make(chan struct{})
	mgr.Gate = gate
	releaseLock := func() {}
	if pgDB != nil {
		releaseLock = holdInstanceLock(pgDB, gate, log, stop)
	} else {
		close(gate)
	}

	mgr.DraftCard = func(ctx context.Context, d store.Draft, name string) error {
		text, btns := botcmd.DraftCard(d, name)
		chat, text := botcmd.Route(ctx, st, d.OwnerID, text)
		return ctrlBot.Ask(ctx, chat, text, btns)
	}

	// ---- Google (ixtiyoriy) ----
	gcli := google.New(env("GOOGLE_CLIENT_ID"), env("GOOGLE_CLIENT_SECRET"), googleRedirect(env("GOOGLE_REDIRECT_URL"), env("MINIAPP_URL")), st, vault, log)
	if gcli != nil {
		if env("GOOGLE_CLIENT_SECRET") == "" {
			return errors.New("GOOGLE_CLIENT_ID berilgan, GOOGLE_CLIENT_SECRET ham kerak")
		}
		// Interfeys maydonlariga faqat yoqilganda qo'yamiz (typed nil != nil).
		mgr.Google = gcli
		log.Info("Google integratsiyasi yoqilgan", "redirect", gcli.RedirectURL)
	}

	ctrlBot.Handler = &botcmd.Handler{
		Store: st, Billing: bill, Ctl: mgr, Admins: admins, Log: log, ICS: icsSigner,
		Notify: func(ctx context.Context, owner int64, text string, openApp bool) { notify(ctx, owner, text, openApp) },
	}

	staticDir := env("STATIC_DIR")
	if staticDir == "" {
		staticDir = "./webapp"
	}
	api := &miniapp.Server{BotToken: env("BOT_TOKEN"), Store: st, Ctl: mgr, Log: log, StaticDir: staticDir,
		PublicURL: publicURL(env("MINIAPP_URL")), ICS: icsSigner}
	if gcli != nil {
		ctrlBot.Handler.(*botcmd.Handler).Google = gcli
		api.Google = gcli
		api.OnGoogle = func(ctx context.Context, owner int64, err error) {
			text := "Google akkauntingiz ulandi ✅ Endi kotib taqvimingizdagi band vaqtlarni hisobga oladi, uchrashuv va vazifalarni tasdiqlashingiz uchun shu yerga yuboradi."
			if err != nil {
				text = "Google ulanmadi. Ilovada qayta urinib ko'ring va taqvim ruxsatini belgilang."
			}
			notify(ctx, owner, text, err != nil)
		}
	}
	if bill.Enabled() {
		api.Billing = bill
	}

	{
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if name, err := ctrlBot.Username(cctx); err == nil {
			api.BotUsername = name
			ctrlBot.Handler.(*botcmd.Handler).BotUsername = name
		} else {
			log.Warn("bot username olinmadi", "err", err)
		}
		cancel()
	}

	addr, err := listenAddr(env("LISTEN"), env("PORT"))
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr: addr, Handler: api.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}

	go func() {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := ctrlBot.SetCommands(cctx, botcmd.Commands); err != nil {
			log.Warn("bot buyruqlari o'rnatilmadi", "err", err)
		}
	}()
	go func() {
		// Telegram getUpdates'ni bir vaqtda faqat bitta nusxaga beradi (aks holda 409 Conflict):
		// bot ham eski nusxa to'xtagach (bitta nusxa qulfi) ishga tushadi.
		select {
		case <-gate:
			ctrlBot.Run(ctx)
		case <-ctx.Done():
		}
	}()
	go janitor(ctx, st, bill, notify, log)
	go insights(ctx, mgr, gate)
	go func() {
		if err := mgr.Start(ctx); err != nil && ctx.Err() == nil {
			log.Error("sessiyalarni ishga tushirish", "err", err)
		}
	}()
	go func() {
		log.Info("HTTP tinglanmoqda", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("to'xtatilmoqda...")
	shCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shCtx)
	mgr.Shutdown()
	releaseLock() // sessiyalar to'liq uzilgandan KEYIN — yangi nusxa shundan so'ng ulanadi
	return nil
}

// instanceLockID — "bitta nusxa" advisory lock'i (migratsiya lock'idan farqli).
const instanceLockID = 7_311_002_402

// holdInstanceLock Postgres advisory lock'ini oladi va gate'ni ochadi. Railway yangi versiyani
// yoqqanda eskisi bir necha soniya ishlab turadi; ikkala nusxa bitta Telegram sessiyasiga ulansa,
// Telegram AUTH_KEY_DUPLICATED bilan sessiyani bekor qiladi. Lock alohida ulanishda ushlanadi;
// ulanish uzilsa (lock yo'qoldi) — jarayon to'xtatiladi, platforma qayta ishga tushiradi.
// Qaytarilgan funksiya lock'ni bo'shatadi (sessiyalar yopilgandan keyin chaqiriladi).
func holdInstanceLock(db *sql.DB, gate chan struct{}, log *slog.Logger, onLost func()) (release func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var conn *sql.Conn
	locked := false
	go func() {
		defer close(done)
		waited := false
		for {
			var err error
			if conn == nil {
				conn, err = db.Conn(ctx)
			}
			var ok bool
			if err == nil {
				err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, instanceLockID).Scan(&ok)
			}
			if err != nil && conn != nil {
				conn.Close()
				conn = nil
			}
			if ok {
				break
			}
			if !waited {
				log.Warn("boshqa nusxa Telegram sessiyalarini ushlab turibdi — u to'xtashi kutilmoqda")
				waited = true
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
		locked = true
		log.Info("bitta nusxa qulfi olindi — Telegram sessiyalari ulanadi")
		close(gate)
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := conn.ExecContext(ctx, `SELECT 1`); err != nil && ctx.Err() == nil {
					log.Error("bitta nusxa qulfi yo'qoldi — jarayon to'xtatiladi", "err", err)
					onLost()
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
		if conn == nil {
			return
		}
		// sql.Conn.Close ulanishni yopmaydi — pulga qaytaradi, lock esa ulanishda qoladi.
		// Shuning uchun lock'ni aniq bo'shatamiz.
		if locked {
			uctx, ucancel := context.WithTimeout(context.Background(), 5*time.Second)
			if _, err := conn.ExecContext(uctx, `SELECT pg_advisory_unlock($1)`, instanceLockID); err != nil {
				log.Warn("bitta nusxa qulfi bo'shatilmadi", "err", err)
			}
			ucancel()
		}
		conn.Close()
	}
}

// intEnv butun sonli env qiymatini o'qiydi (bo'sh → def) va oraliqni tekshiradi.
func intEnv(key string, def, min, max int) (int, error) {
	v := env(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s %d–%d oralig'idagi butun son bo'lishi kerak", key, min, max)
	}
	return n, nil
}

// parseIDs "123,456" → {123, 456} (ADMIN_IDS).
func parseIDs(v string) (map[int64]bool, error) {
	out := map[int64]bool{}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("ADMIN_IDS noto'g'ri: %q (vergul bilan ajratilgan Telegram ID'lar)", part)
		}
		out[id] = true
	}
	return out, nil
}

// publicURL — MINIAPP_URL'ning domeni (https://domen).
func publicURL(miniApp string) string {
	u, err := url.Parse(miniApp)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// googleRedirect — OAuth qaytish manzili: aniq GOOGLE_REDIRECT_URL yoki MINIAPP_URL domeni.
func googleRedirect(explicit, miniApp string) string {
	if explicit != "" {
		return explicit
	}
	return publicURL(miniApp) + "/oauth/google/callback"
}

// listenAddr tinglash manzilini tanlaydi: aniq LISTEN > PORT (Railway/Heroku kabi
// platformalar beradi, barcha interfeyslarda) > 127.0.0.1:8080 (TLS'ni reverse proxy tugatadi).
func listenAddr(listen, port string) (string, error) {
	if listen != "" {
		return listen, nil
	}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "", errors.New("PORT 1–65535 oralig'idagi raqam bo'lishi kerak")
		}
		return "0.0.0.0:" + port, nil
	}
	return "127.0.0.1:8080", nil
}

// insights har 5 daqiqada: muddati kelgan "keyinroq yozaman" va'dalarini tekshiradi va
// soat 21:00 da kechki xulosani yuboradi. Faqat bitta nusxa (gate) ishlatadi — takror yuborilmasin.
func insights(ctx context.Context, mgr *userbot.Manager, gate <-chan struct{}) {
	select {
	case <-gate:
	case <-ctx.Done():
		return
	}
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		now := time.Now()
		mgr.CheckFollowups(ctx, now)
		mgr.SendDigests(ctx, now)
		mgr.SendWeeklyReports(ctx, now)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// janitor maxfiylik uchun eski ma'lumotni muntazam tozalaydi: draftlar 7 kun,
// audit 90 kun. Har soatda (va ishga tushganda bir marta) ishlaydi.
func janitor(ctx context.Context, st store.Store, bill *billing.Service, notify billing.Notifier, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		now := time.Now()
		if n, err := st.ExpireDrafts(ctx, now.Add(-draftTTL)); err != nil {
			log.Warn("janitor drafts", "err", err)
		} else if n > 0 {
			log.Info("janitor: eski draftlar tozalandi", "n", n)
		}
		if _, err := st.PruneAudit(ctx, now.Add(-auditTTL)); err != nil {
			log.Warn("janitor audit", "err", err)
		}
		if _, err := st.PruneFollowups(ctx, now.Add(-draftTTL)); err != nil {
			log.Warn("janitor followups", "err", err)
		}
		if _, err := st.PruneActions(ctx, now.Add(-draftTTL)); err != nil {
			log.Warn("janitor actions", "err", err)
		}
		bill.RemindExpiring(ctx, notify)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
