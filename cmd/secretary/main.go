package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/bot"
	"github.com/ozodjongg/secretar_ai_agent/internal/crypto"
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

	keys, err := crypto.NewStaticKeyFromBase64(env("MASTER_KEY"))
	if err != nil {
		return err
	}
	vault := crypto.NewVault(keys)

	// ---- Saqlash ----
	var st store.Store
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
	notify := func(ctx context.Context, owner int64, text string, openApp bool) {
		if err := ctrlBot.Notify(ctx, owner, text, openApp); err != nil {
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

	mgr := &userbot.Manager{
		Store: st, Conn: conn, Log: log, Notify: notify,
		LLM: llm.NewGemini(env("GEMINI_API_KEY"), env("GEMINI_MODEL")),
	}

	staticDir := env("STATIC_DIR")
	if staticDir == "" {
		staticDir = "./webapp"
	}
	api := &miniapp.Server{BotToken: env("BOT_TOKEN"), Store: st, Ctl: mgr, Log: log, StaticDir: staticDir}

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

	go ctrlBot.Run(ctx)
	go janitor(ctx, st, log)
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
	return nil
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

// janitor maxfiylik uchun eski ma'lumotni muntazam tozalaydi: draftlar 7 kun,
// audit 90 kun. Har soatda (va ishga tushganda bir marta) ishlaydi.
func janitor(ctx context.Context, st store.Store, log *slog.Logger) {
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
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
