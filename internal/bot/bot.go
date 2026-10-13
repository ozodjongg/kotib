// Package bot: boshqaruv boti (Bot API, faqat stdlib). Vazifasi:
//   - /start → Mini App'ni ochish tugmasi;
//   - userbot'dan mijozga bildirishnoma (draft tayyor, akkaunt to'xtadi va h.k.).
//
// Mijozning shaxsiy xabarlari bu bot orqali O'TMAYDI — ular faqat userbot sessiyasida.
package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type Bot struct {
	Token      string
	MiniAppURL string // https://.../ (Telegram faqat HTTPS qabul qiladi)
	HTTP       *http.Client
	Log        *slog.Logger
	BaseURL    string // test uchun; default https://api.telegram.org
}

func New(token, miniAppURL string, log *slog.Logger) *Bot {
	return &Bot{
		Token: token, MiniAppURL: miniAppURL, Log: log,
		HTTP:    &http.Client{Timeout: 40 * time.Second},
		BaseURL: "https://api.telegram.org",
	}
}

func (b *Bot) call(ctx context.Context, method string, payload any, out any) error {
	raw, _ := json.Marshal(payload)
	// Token URL'da (Bot API shunday talab qiladi) — xato matniga URL kiritmaymiz.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.BaseURL+"/bot"+b.Token+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return errors.New("bot: so'rov yaratilmadi")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("bot %s: tarmoq xatosi", method)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(data, &env); err != nil || !env.OK {
		return fmt.Errorf("bot %s: %d %s", method, resp.StatusCode, env.Description)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

type inlineBtn struct {
	Text   string      `json:"text"`
	WebApp *webAppInfo `json:"web_app,omitempty"`
}
type webAppInfo struct {
	URL string `json:"url"`
}

// Notify mijozga xabar yuboradi; openApp=true bo'lsa "Ochish" (Mini App) tugmasi bilan.
// Xabar matniga kontakt xabarlarini QO'SHMANG — bildirishnomalar faqat meta ma'lumot.
func (b *Bot) Notify(ctx context.Context, chatID int64, text string, openApp bool) error {
	p := map[string]any{"chat_id": chatID, "text": text}
	if openApp && b.MiniAppURL != "" {
		p["reply_markup"] = map[string]any{
			"inline_keyboard": [][]inlineBtn{{{Text: "Ochish", WebApp: &webAppInfo{URL: b.MiniAppURL}}}},
		}
	}
	return b.call(ctx, "sendMessage", p, nil)
}

type update struct {
	UpdateID int `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
	} `json:"message"`
}

// Run long-polling bilan /start'ga javob beradi. ctx bekor qilinganda to'xtaydi.
func (b *Bot) Run(ctx context.Context) {
	offset := 0
	backoff := time.Second
	for ctx.Err() == nil {
		var ups []update
		err := b.call(ctx, "getUpdates", map[string]any{
			"offset": offset, "timeout": 30, "allowed_updates": []string{"message"},
		}, &ups)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.Log.Warn("bot getUpdates", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			offset = u.UpdateID + 1
			if u.Message == nil || u.Message.Chat.Type != "private" {
				continue
			}
			if t := u.Message.Text; len(t) >= 6 && t[:6] == "/start" {
				if err := b.Notify(ctx, u.Message.Chat.ID,
					"Salom! Kotibni sozlash va draftlarni ko'rish uchun pastdagi tugmani bosing.", true); err != nil {
					b.Log.Warn("bot /start", "err", err)
				}
			}
		}
	}
}
