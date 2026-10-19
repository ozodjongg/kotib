package userbot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/guard"
	"github.com/ozodjongg/secretar_ai_agent/internal/ratelimit"
)

// Rejalashtirilgan vazifa natijasini egasining kontaktiga yuborish.

// MaxTaskSendsPerDay — vazifalar egasining akkauntidan kuniga ko'pi bilan shuncha xabarni
// tasdiqsiz yuboradi (Telegram spam deb hisoblamasin); ortig'i qoralama bo'ladi.
const MaxTaskSendsPerDay = 20

// ErrNotConnected — Telegram sessiyasi hozir ulanmagan (kotib to'xtatilgan yoki uzilgan).
var ErrNotConnected = errors.New("Telegram akkaunt hozir ulanmagan — kotib yoqilganini tekshiring")

// Deliver vazifa natijasini chatID kontaktiga yuboradi. auto=false yoki xavfsizlik shartlari
// bajarilmasa (filtr, qoida, kunlik limit, tezlik, Telegram kutishi) — egasiga qoralama bo'ladi.
// sent — xabar kontaktga ketdi; false bo'lsa qoralama yaratildi (egasiga xabar boradi).
func (m *Manager) Deliver(ctx context.Context, owner, chatID int64, name, title, text string, auto bool) (sent bool, err error) {
	if chatID == 0 || chatID == owner || config.IsTelegramService(chatID) {
		return false, errors.New("qabul qiluvchi noto'g'ri")
	}
	tg, pipe := m.live(owner)
	if tg == nil || pipe == nil {
		return false, ErrNotConnected
	}
	pipe.init()
	in := Incoming{ChatID: chatID, Text: "⏰ Vazifa: " + title, Contact: config.Contact{UserID: chatID, Name: name, IsContact: true}}
	if !auto {
		pipe.makeDraft(ctx, in, text, "rejalashtirilgan vazifa")
		return false, nil
	}
	st, err := m.Store.GetSettings(ctx, owner)
	if err != nil {
		return false, err
	}
	if reason := m.taskSendBlock(ctx, owner, chatID, &st, pipe, text); reason != "" {
		pipe.makeDraft(ctx, in, text, reason)
		return false, nil
	}
	_, typing := ratelimit.HumanDelay("", text)
	if err := tg.SendReply(ctx, chatID, 0, text, typing); err != nil {
		pipe.handleTGErr(ctx, err)
		pipe.makeDraft(ctx, in, text, "yuborilmadi")
		return false, nil
	}
	m.Store.Audit(ctx, owner, "schedule_sent", fmt.Sprintf("chat=%d", chatID))
	pipe.markSent(ctx, chatID, text)
	return true, nil
}

// taskSendBlock — tasdiqsiz yuborishga to'sqinlik sababi ("" — yuborish mumkin).
func (m *Manager) taskSendBlock(ctx context.Context, owner, chatID int64, st *config.Settings, pipe *Pipeline, text string) string {
	if ok, why := guard.CheckOutput(text, st.AllowLinks); !ok {
		m.Store.Audit(ctx, owner, "output_rejected", why)
		return "javob filtridan o'tmadi: " + why
	}
	if !m.chatAllowed(st, chatID) {
		return "qoida bo'yicha bu kontaktga yozish o'chirilgan"
	}
	if pipe.isPeerSecretary(chatID) {
		return PeerSecretaryReason
	}
	now := pipe.Now()
	if now.Before(pipe.blockedUntilTime()) {
		return "Telegram kutishni so'radi"
	}
	loc := st.Location()
	l := now.In(loc)
	day := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	if n, err := m.Store.CountAudit(ctx, owner, []string{"schedule_sent"}, day); err != nil || n >= MaxTaskSendsPerDay {
		return "vazifalarning kunlik yuborish limiti"
	}
	if !pipe.Limiter.Allow(chatID) {
		return "tezlik limiti"
	}
	return ""
}
