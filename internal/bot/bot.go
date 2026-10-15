// Package bot: boshqaruv boti (Bot API, faqat stdlib). Vazifasi:
//   - buyruqlar (/start, /status, /pause...) → Handler;
//   - userbot'dan mijozga bildirishnoma (draft tayyor, akkaunt to'xtadi va h.k.);
//   - Telegram Stars to'lovlari (invoice, pre_checkout, successful_payment, refund).
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
	"net/url"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/billing"
)

type Bot struct {
	Token      string
	MiniAppURL string // https://.../ (Telegram faqat HTTPS qabul qiladi)
	HTTP       *http.Client
	Log        *slog.Logger
	BaseURL    string // test uchun; default https://api.telegram.org
	Handler    Handler
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
		return fmt.Errorf("bot %s: tarmoq xatosi: %s", method, b.redact(err))
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

// redact xato matnidan URL va tokenni olib tashlaydi: *url.Error to'liq URL'ni
// (ichida bot tokeni) o'z ichiga oladi — logga faqat ichki sabab yoziladi.
func (b *Bot) redact(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	if b.Token != "" {
		msg = strings.ReplaceAll(msg, b.Token, "***")
	}
	return msg
}

type inlineBtn struct {
	Text   string      `json:"text"`
	WebApp *webAppInfo `json:"web_app,omitempty"`
	URL    string      `json:"url,omitempty"`
	Data   string      `json:"callback_data,omitempty"`
}
type webAppInfo struct {
	URL string `json:"url"`
}

// Notify mijozga xabar yuboradi; openApp=true bo'lsa "Ochish" (Mini App) tugmasi bilan.
// Xabar matniga kontakt xabarlarini QO'SHMANG — bildirishnomalar faqat meta ma'lumot.
func (b *Bot) Notify(ctx context.Context, chatID int64, text string, openApp bool) error {
	return b.send(ctx, chatID, Reply{Text: text, OpenApp: openApp})
}

// Reply — buyruqqa javob.
type Reply struct {
	Text      string
	OpenApp   bool   // "Ochish" (Mini App) tugmasi
	ButtonURL string // ixtiyoriy havola tugmasi (masalan, to'lov)
	Button    string // ButtonURL matni
	Links     []Link // qo'shimcha havola tugmalari, har biri alohida qatorda (masalan, tariflar)
}

// Link — havola tugmasi.
type Link struct{ Text, URL string }

func (b *Bot) send(ctx context.Context, chatID int64, r Reply) error {
	p := map[string]any{"chat_id": chatID, "text": r.Text}
	var row []inlineBtn
	if r.ButtonURL != "" {
		row = append(row, inlineBtn{Text: r.Button, URL: r.ButtonURL})
	}
	if r.OpenApp && b.MiniAppURL != "" {
		row = append(row, inlineBtn{Text: "Ochish", WebApp: &webAppInfo{URL: b.MiniAppURL}})
	}
	var rows [][]inlineBtn
	for _, l := range r.Links {
		rows = append(rows, []inlineBtn{{Text: l.Text, URL: l.URL}})
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	if len(rows) > 0 {
		p["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	return b.call(ctx, "sendMessage", p, nil)
}

// Answer — tugma bosilishiga javob. Edit bo'sh bo'lsa xabar o'zgarmaydi (tugmalar qoladi);
// aks holda tugmalar olib tashlanadi, URL berilsa o'rniga havola tugmasi qo'yiladi.
type Answer struct {
	Toast, Edit  string
	URL, URLText string
}

// Button — callback tugmasi (Data ≤ 64 bayt).
type Button struct{ Text, Data string }

// Ask mijozga tugmali xabar yuboradi (masalan, "Taqvimga qo'shaymi?").
func (b *Bot) Ask(ctx context.Context, chatID int64, text string, buttons []Button) error {
	row := make([]inlineBtn, 0, len(buttons))
	for _, btn := range buttons {
		row = append(row, inlineBtn{Text: btn.Text, Data: btn.Data})
	}
	return b.call(ctx, "sendMessage", map[string]any{
		"chat_id": chatID, "text": text,
		"reply_markup": map[string]any{"inline_keyboard": [][]inlineBtn{row}},
	}, nil)
}

// ---- Telegram Stars (billing.API) ----

var _ billing.API = (*Bot)(nil)

func (b *Bot) CreateInvoiceLink(ctx context.Context, in billing.InvoiceLink) (string, error) {
	var link string
	err := b.call(ctx, "createInvoiceLink", map[string]any{
		"title":               in.Title,
		"description":         in.Description,
		"payload":             in.Payload,
		"currency":            billing.Currency,
		"prices":              []map[string]any{{"label": in.Label, "amount": in.Amount}},
		"subscription_period": in.SubscriptionPeriod,
	}, &link)
	return link, err
}

func (b *Bot) EditUserStarSubscription(ctx context.Context, userID int64, chargeID string, canceled bool) error {
	return b.call(ctx, "editUserStarSubscription", map[string]any{
		"user_id": userID, "telegram_payment_charge_id": chargeID, "is_canceled": canceled,
	}, nil)
}

func (b *Bot) RefundStarPayment(ctx context.Context, userID int64, chargeID string) error {
	return b.call(ctx, "refundStarPayment", map[string]any{
		"user_id": userID, "telegram_payment_charge_id": chargeID,
	}, nil)
}

// Username botning @username'ini qaytaradi (getMe).
func (b *Bot) Username(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	err := b.call(ctx, "getMe", map[string]any{}, &me)
	return me.Username, err
}

// Command — bot menyusidagi buyruq.
type Command struct {
	Name, Description string
}

// SetCommands bot menyusini ("/" tugmasi) o'rnatadi.
func (b *Bot) SetCommands(ctx context.Context, cmds []Command) error {
	list := make([]map[string]string, 0, len(cmds))
	for _, c := range cmds {
		list = append(list, map[string]string{"command": c.Name, "description": c.Description})
	}
	return b.call(ctx, "setMyCommands", map[string]any{"commands": list}, nil)
}

// ---- Update'larni qayta ishlash ----

// Handler — buyruqlar va to'lov hodisalari. Barcha "from" qiymatlari Telegram
// update'idan olinadi (soxtalashtirib bo'lmaydi).
type Handler interface {
	// Command shaxsiy chatdagi "/buyruq args"ga javob; bo'sh Text — javobsiz.
	Command(ctx context.Context, from int64, cmd, args string) Reply
	// PreCheckout nil qaytarsa to'lovga ruxsat beriladi; aks holda xato matni mijozga ko'rsatiladi.
	PreCheckout(ctx context.Context, from int64, currency string, amount int, payload string) error
	Paid(ctx context.Context, from int64, p billing.PaymentInfo)
	Refunded(ctx context.Context, from int64, chargeID string)
	// ReplyTo — foydalanuvchi bot xabariga javob yozdi. data — o'sha xabardagi birinchi
	// tugmaning callback_data'si (qaysi qoralama ekanini bildiradi); edit — asl xabarning
	// yangi matni (bo'sh — o'zgarmaydi).
	ReplyTo(ctx context.Context, from int64, data, text string) (r Reply, edit string)
	// Callback tugma bosilganda: qisqa javob, xabarning yangi matni va ixtiyoriy havola tugmasi.
	Callback(ctx context.Context, from int64, data string) Answer
}

type user struct {
	ID int64 `json:"id"`
}

type successfulPayment struct {
	Currency                   string `json:"currency"`
	TotalAmount                int    `json:"total_amount"`
	InvoicePayload             string `json:"invoice_payload"`
	SubscriptionExpirationDate int64  `json:"subscription_expiration_date"`
	IsRecurring                bool   `json:"is_recurring"`
	IsFirstRecurring           bool   `json:"is_first_recurring"`
	TelegramPaymentChargeID    string `json:"telegram_payment_charge_id"`
}

type refundedPayment struct {
	TelegramPaymentChargeID string `json:"telegram_payment_charge_id"`
}

type update struct {
	UpdateID int `json:"update_id"`
	Message  *struct {
		Text           string `json:"text"`
		From           *user  `json:"from"`
		ReplyToMessage *struct {
			MessageID int   `json:"message_id"`
			From      *user `json:"from"`
			Markup    *struct {
				Keyboard [][]struct {
					Data string `json:"callback_data"`
				} `json:"inline_keyboard"`
			} `json:"reply_markup"`
		} `json:"reply_to_message"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		SuccessfulPayment *successfulPayment `json:"successful_payment"`
		RefundedPayment   *refundedPayment   `json:"refunded_payment"`
	} `json:"message"`
	CallbackQuery *struct {
		ID      string `json:"id"`
		From    user   `json:"from"`
		Data    string `json:"data"`
		Message *struct {
			MessageID int    `json:"message_id"`
			Text      string `json:"text"`
			Chat      struct {
				ID   int64  `json:"id"`
				Type string `json:"type"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
	PreCheckoutQuery *struct {
		ID             string `json:"id"`
		From           user   `json:"from"`
		Currency       string `json:"currency"`
		TotalAmount    int    `json:"total_amount"`
		InvoicePayload string `json:"invoice_payload"`
	} `json:"pre_checkout_query"`
}

// Run long-polling bilan update'larni qabul qiladi. ctx bekor qilinganda to'xtaydi.
func (b *Bot) Run(ctx context.Context) {
	offset := 0
	backoff := time.Second
	for ctx.Err() == nil {
		var ups []update
		err := b.call(ctx, "getUpdates", map[string]any{
			"offset": offset, "timeout": 30, "allowed_updates": []string{"message", "pre_checkout_query", "callback_query"},
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
			b.dispatch(ctx, u)
		}
	}
}

func (b *Bot) dispatch(ctx context.Context, u update) {
	if q := u.CallbackQuery; q != nil {
		b.callback(ctx, u)
		return
	}
	if q := u.PreCheckoutQuery; q != nil {
		// Telegram 10 soniya ichida javob kutadi — sinxron javob beramiz.
		ans := map[string]any{"pre_checkout_query_id": q.ID, "ok": true}
		if b.Handler == nil {
			ans["ok"], ans["error_message"] = false, "To'lov hozircha qabul qilinmaydi."
		} else if err := b.Handler.PreCheckout(ctx, q.From.ID, q.Currency, q.TotalAmount, q.InvoicePayload); err != nil {
			ans["ok"], ans["error_message"] = false, err.Error()
		}
		if err := b.call(ctx, "answerPreCheckoutQuery", ans, nil); err != nil {
			b.Log.Warn("answerPreCheckoutQuery", "err", err)
		}
		return
	}
	m := u.Message
	if m == nil || m.Chat.Type != "private" || m.From == nil {
		return
	}
	from := m.From.ID
	if sp := m.SuccessfulPayment; sp != nil && b.Handler != nil {
		p := billing.PaymentInfo{
			Currency: sp.Currency, Amount: sp.TotalAmount, Payload: sp.InvoicePayload,
			ChargeID: sp.TelegramPaymentChargeID, Recurring: sp.IsRecurring, FirstRecurring: sp.IsFirstRecurring,
		}
		if sp.SubscriptionExpirationDate > 0 {
			p.ExpiresAt = time.Unix(sp.SubscriptionExpirationDate, 0)
		}
		b.Handler.Paid(ctx, from, p)
		return
	}
	if rp := m.RefundedPayment; rp != nil && b.Handler != nil {
		b.Handler.Refunded(ctx, from, rp.TelegramPaymentChargeID)
		return
	}
	cmd, args, ok := parseCommand(m.Text)
	if !ok {
		b.replyTo(ctx, u)
		return
	}
	var r Reply
	if b.Handler != nil {
		r = b.Handler.Command(ctx, from, cmd, args)
	} else if cmd == "start" {
		r = Reply{Text: "Salom! Kotibni sozlash va qoralamalarni ko'rish uchun pastdagi tugmani bosing.", OpenApp: true}
	}
	if r.Text == "" {
		return
	}
	if err := b.send(ctx, m.Chat.ID, r); err != nil {
		b.Log.Warn("bot reply", "cmd", cmd, "err", err)
	}
}

func (b *Bot) callback(ctx context.Context, u update) {
	q := u.CallbackQuery
	var a Answer
	// Faqat shaxsiy chatdagi o'z xabarimiz: from — Telegram tasdiqlagan foydalanuvchi.
	if b.Handler != nil && len(q.Data) <= 64 && (q.Message == nil || q.Message.Chat.Type == "private") {
		a = b.Handler.Callback(ctx, q.From.ID, q.Data)
	}
	if err := b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": q.ID, "text": a.Toast}, nil); err != nil {
		b.Log.Warn("answerCallbackQuery", "err", err)
	}
	if a.Edit != "" && q.Message != nil {
		// Tugmalar olib tashlanadi — qayta bosib bo'lmasin.
		p := map[string]any{"chat_id": q.Message.Chat.ID, "message_id": q.Message.MessageID, "text": a.Edit}
		if a.URL != "" {
			p["reply_markup"] = map[string]any{"inline_keyboard": [][]inlineBtn{{{Text: a.URLText, URL: a.URL}}}}
		}
		if err := b.call(ctx, "editMessageText", p, nil); err != nil {
			b.Log.Warn("editMessageText", "err", err)
		}
	}
}

// replyTo — bot xabariga (masalan, qoralama kartasiga) matnli javob.
func (b *Bot) replyTo(ctx context.Context, u update) {
	m := u.Message
	rt := m.ReplyToMessage
	if b.Handler == nil || rt == nil || rt.Markup == nil || len(rt.Markup.Keyboard) == 0 ||
		len(rt.Markup.Keyboard[0]) == 0 || strings.TrimSpace(m.Text) == "" {
		return
	}
	r, edit := b.Handler.ReplyTo(ctx, m.From.ID, rt.Markup.Keyboard[0][0].Data, m.Text)
	if edit != "" {
		if err := b.call(ctx, "editMessageText", map[string]any{
			"chat_id": m.Chat.ID, "message_id": rt.MessageID, "text": edit,
		}, nil); err != nil {
			b.Log.Warn("editMessageText", "err", err)
		}
	}
	if r.Text != "" {
		if err := b.send(ctx, m.Chat.ID, r); err != nil {
			b.Log.Warn("bot reply", "err", err)
		}
	}
}

// parseCommand "/cmd@BotName args" → ("cmd", "args").
func parseCommand(text string) (cmd, args string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") || len(text) > 512 {
		return "", "", false
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, _, _ = strings.Cut(head, "@")
	head = strings.ToLower(head)
	if head == "" || len(head) > 32 {
		return "", "", false
	}
	return head, strings.TrimSpace(rest), true
}
