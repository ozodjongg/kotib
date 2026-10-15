package botcmd

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/bot"
	"github.com/ozodjongg/secretar_ai_agent/internal/userbot"
)

// askTimeout — /sor javobi uchun vaqt (yozishmani o'qish + model).
const askTimeout = 90 * time.Second

const askUsage = "Savolni kontakt ismi bilan yozing, masalan:\n/sor Aziz bilan nima kelishdik?\n/sor Onam ertaga nima olib kelishimni so'radi?"

// ask — /sor: javob bir necha soniya oladi, shuning uchun fonda tayyorlanadi va bot orqali yuboriladi
// (bot update'larni ketma-ket qayta ishlaydi — uzoq kutish boshqa tugmalarni to'xtatmasin).
func (h *Handler) ask(ctx context.Context, from int64, question string) bot.Reply {
	question = strings.TrimSpace(question)
	if h.AI == nil {
		return bot.Reply{Text: "Bu funksiya hozircha o'chiq."}
	}
	if question == "" {
		return bot.Reply{Text: askUsage}
	}
	if len([]rune(question)) > 500 {
		return bot.Reply{Text: "Savol juda uzun (500 belgigacha)."}
	}
	go func() {
		actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), askTimeout)
		defer cancel()
		text := h.answer(actx, from, question)
		if h.Notify != nil {
			h.Notify(actx, from, text, false)
		}
	}()
	return bot.Reply{Text: "🔎 Yozishmani o'qiyapman..."}
}

// answer — savolni asosiy akkauntda, topilmasa qo'shimcha akkauntlarda qidiradi.
func (h *Handler) answer(ctx context.Context, from int64, question string) string {
	var lastErr error
	for _, owner := range h.accounts(ctx, from) {
		ans, err := h.AI.Ask(ctx, owner, question)
		if err == nil {
			return ans
		}
		lastErr = err
		if !errors.Is(err, userbot.ErrNoContact) && !errors.Is(err, userbot.ErrNoLiveSession) {
			break
		}
	}
	switch {
	case errors.Is(lastErr, userbot.ErrNeedPro):
		return "/sor — Pro tarif funksiyasi. Tarifni almashtirish: /obuna"
	case errors.Is(lastErr, userbot.ErrNoContact):
		if msg := lastErr.Error(); strings.Contains(msg, "yopiq") {
			return "Bu kontakt bilan yozishma qoidalaringiz bo'yicha kotibdan yopiq."
		}
		return "Savolda kontakt ismini topmadim.\n\n" + askUsage
	case errors.Is(lastErr, userbot.ErrNoLiveSession):
		return "Kotib hozir Telegram'ga ulanmagan. /status orqali tekshiring."
	default:
		h.log().Warn("ask", "owner", from, "err", lastErr)
		return "Javob tayyorlab bo'lmadi, keyinroq urinib ko'ring."
	}
}

// shadowCallback — soya rejimi natijasidagi tugma: "shd:auto:<akkaunt>" yoki "shd:draft:<akkaunt>".
func (h *Handler) shadowCallback(ctx context.Context, from int64, data string) (string, string) {
	if h.AI == nil {
		return "", ""
	}
	i := strings.LastIndexByte(data, ':')
	choice, sid := data[:i], data[i+1:]
	owner, err := strconv.ParseInt(sid, 10, 64)
	if err != nil || (choice != userbot.ShadowAuto && choice != userbot.ShadowDraft) {
		return "", ""
	}
	allowed := false
	for _, o := range h.accounts(ctx, from) {
		allowed = allowed || o == owner
	}
	if !allowed {
		return "Topilmadi", ""
	}
	msg, err := h.AI.ApplyShadowChoice(ctx, owner, choice)
	if err != nil {
		h.log().Warn("shadow choice", "owner", owner, "err", err)
		return "Xatolik", ""
	}
	return "Saqlandi", msg
}
