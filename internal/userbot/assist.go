package userbot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/guard"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// analyst — model egasi uchun topshiriqlarni (xulosa, transkript...) qo'llasa.
func analystOf(g llm.Generator) (llm.Analyst, bool) {
	a, ok := g.(llm.Analyst)
	return a, ok
}

// recordIn kontakt xabarini chat faolligiga yozadi (matnsiz). Bot, guruh va kanallar yozilmaydi.
func (p *Pipeline) recordIn(ctx context.Context, in Incoming, private bool) {
	c := in.Contact
	if c.IsBot || c.IsGroup || c.IsChannel || config.IsTelegramService(c.UserID) {
		return
	}
	if err := p.Store.ChatIn(ctx, store.ChatActivity{
		OwnerID: p.Owner, ChatID: in.ChatID, Name: c.Name, Private: private, LastIn: p.Now(),
	}); err != nil {
		p.Log.Debug("chat activity", "owner", p.Owner, "err", err)
	}
}

// urgentEvery — bitta chatdan shoshilinch ogohlantirish shundan tez-tez yuborilmaydi.
const urgentEvery = 30 * time.Minute

// alertUrgent — shoshilinch xabar haqida egasiga darhol bildiradi (tinch soatlarda ham).
// Xabar matni bot chatiga faqat "Qoralamalar bot chatida" yoqilgan bo'lsa qo'shiladi.
func (p *Pipeline) alertUrgent(ctx context.Context, st *config.Settings, in Incoming) {
	if st.NoUrgent || in.Contact.IsBot || !guard.IsUrgent(in.Text) {
		return
	}
	name := strings.TrimSpace(in.Contact.Name)
	if name == "" {
		name = "Kontakt"
	}
	text := "🚨 " + name + " shoshilinch yozdi."
	if st.DraftsInBot {
		text += "\n\n💬 " + clip(in.Text, 500)
	}
	if p.notifyOnce(ctx, fmt.Sprintf("urgent:%d", in.ChatID), urgentEvery, text, true) {
		p.Store.Audit(ctx, p.Owner, "urgent_alert", fmt.Sprintf("chat=%d", in.ChatID))
	}
}

// loadVoice ovozli faylni yuklaydi; xato bo'lsa auditga yozib nil qaytaradi.
func (p *Pipeline) loadVoice(ctx context.Context, v *Voice) *llm.Audio {
	vctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	data, err := v.Load(vctx)
	cancel()
	if err != nil || len(data) == 0 {
		p.Store.Audit(ctx, p.Owner, "voice_error", "")
		p.Log.Warn("voice", "owner", p.Owner, "err", err)
		return nil
	}
	return &llm.Audio{MIME: "audio/ogg", Data: data}
}

// transcribe ovozli xabarni matnga o'girib egasiga yuboradi (Pro, sozlama yoqilgan bo'lsa).
// Yuklangan audio qaytariladi — javob tayyorlashda qayta yuklanmasin.
func (p *Pipeline) transcribe(ctx context.Context, st *config.Settings, in Incoming, pro bool) *llm.Audio {
	an, ok := analystOf(p.LLM)
	if in.Voice == nil || !st.TranscribeVoice || !pro || !ok || p.Notify == nil || in.Voice.Duration > maxTranscribeSeconds {
		return nil
	}
	audio := p.loadVoice(ctx, in.Voice)
	if audio == nil {
		return nil
	}
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	text, err := an.Transcribe(tctx, audio)
	cancel()
	if err != nil {
		p.Store.Audit(ctx, p.Owner, "llm_error", "transcribe")
		p.Log.Warn("transcribe", "owner", p.Owner, "err", err)
		return audio
	}
	name := strings.TrimSpace(in.Contact.Name)
	if name == "" {
		name = "Kontakt"
	}
	d := in.Voice.Duration
	p.Store.Audit(ctx, p.Owner, "voice_transcribed", fmt.Sprintf("chat=%d", in.ChatID))
	p.Notify(ctx, p.Owner, fmt.Sprintf("🎤 %s (%d:%02d):\n«%s»", name, d/60, d%60, text), false)
	return audio
}

// rememberShadow soya rejimidagi taxminni saqlaydi (xotirada, chegaralangan).
func (p *Pipeline) rememberShadow(in Incoming, reply string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.Now()
	if _, ok := p.shadow[in.ChatID]; !ok && len(p.shadow) >= maxShadow {
		for id, g := range p.shadow { // eskirganlarini tozalaymiz; joy bo'lmasa — tashlaymiz
			if now.Sub(g.at) > shadowTTL {
				delete(p.shadow, id)
			}
		}
		if len(p.shadow) >= maxShadow {
			return
		}
	}
	p.shadow[in.ChatID] = shadowGuess{incoming: in.Text, reply: reply, at: now}
}

// markSent — kotib yuborgan xabar: faollikka yoziladi va keyin "egasi yozdi" deb qayta sanalmaydi.
func (p *Pipeline) markSent(ctx context.Context, chatID int64, text string) {
	now := p.Now()
	p.mu.Lock()
	marks := p.ownSent[chatID][:0:0]
	for _, m := range p.ownSent[chatID] {
		if now.Sub(m.at) < ownSentTTL {
			marks = append(marks, m)
		}
	}
	if len(marks) < 20 {
		marks = append(marks, sentMark{hash: textHash(text), at: now})
	}
	p.ownSent[chatID] = marks
	if len(p.ownSent) > maxPendingChats {
		for id, ms := range p.ownSent {
			if len(ms) == 0 || now.Sub(ms[len(ms)-1].at) >= ownSentTTL {
				delete(p.ownSent, id)
			}
		}
	}
	p.mu.Unlock()
	_ = p.Store.ChatOut(ctx, p.Owner, chatID, now)
}

func (p *Pipeline) isOwnSend(chatID int64, text string) bool {
	h, now := textHash(text), p.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range p.ownSent[chatID] {
		if m.hash == h && now.Sub(m.at) < ownSentTTL {
			return true
		}
	}
	return false
}

// OwnerMessage — egasi chatga o'zi yozdi (boshqa qurilmadan). Faollik yangilanadi; soya
// rejimida kotib taxmini bo'lsa — juftlik o'qitish uchun saqlanadi.
func (p *Pipeline) OwnerMessage(ctx context.Context, chatID int64, text string) {
	p.init()
	if p.isOwnSend(chatID, text) {
		return // kotibning o'z xabari qaytib keldi
	}
	p.resetAutoStreak(chatID)
	now := p.Now()
	_ = p.Store.ChatOut(ctx, p.Owner, chatID, now)
	p.mu.Lock()
	g, ok := p.shadow[chatID]
	delete(p.shadow, chatID)
	p.mu.Unlock()
	if !ok || now.Sub(g.at) > shadowTTL || strings.TrimSpace(text) == "" {
		return
	}
	if err := p.Store.AddStyleSample(ctx, store.StyleSample{
		OwnerID: p.Owner, Kind: store.SampleShadow, Incoming: g.incoming, Predicted: g.reply, Actual: text,
	}); err != nil {
		p.Log.Warn("style sample", "owner", p.Owner, "err", err)
	}
}

// learnFromEdit — egasi qoralamani o'zgartirib yubordi: o'qitish rejimida juftlik saqlanadi.
func (p *Pipeline) learnFromEdit(ctx context.Context, d store.Draft) {
	if d.ID == 0 {
		return
	}
	st, err := p.Store.GetSettings(ctx, p.Owner)
	if err != nil || !(st.Learn || st.ShadowActive(p.Now())) {
		return
	}
	orig, err := p.Store.GetDraft(ctx, p.Owner, d.ID)
	if err != nil || textHash(orig.Reply) == textHash(d.Reply) {
		return
	}
	if err := p.Store.AddStyleSample(ctx, store.StyleSample{
		OwnerID: p.Owner, Kind: store.SampleEdit, Incoming: orig.Incoming, Predicted: orig.Reply, Actual: d.Reply,
	}); err != nil {
		p.Log.Warn("style sample", "owner", p.Owner, "err", err)
	}
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
