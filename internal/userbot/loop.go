package userbot

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Sikl himoyasi: ikki tomon ham avtomatik javob bersa (ikkinchi tomonda ham kotib, boshqa
// AI-javobchi yoki avto-javob), har javob qarshi tomonda yangi javobni keltirib chiqaradi va
// suhbat cheksiz davom etadi — tezlik limiti uni faqat sekinlashtiradi (kuniga yuzlab xabar,
// PEER_FLOOD/ban xavfi, model xarajati). Shuning uchun bitta chatda egasi aralashmasdan
// ketma-ket yuborilgan avtomatik javoblar sanaladi; chegaradan oshsa avtomatik yuborish
// to'xtaydi — keyingi javoblar qoralama bo'ladi va egasiga bildiriladi. Egasi o'zi yozsa
// yoki qoralamani tasdiqlasa, hisob qayta boshlanadi.
//
// Qarshi tomon ham shu xizmatda kotib ulagan foydalanuvchi bo'lsa — eng xavfli holat — unga
// avtomatik javob umuman yuborilmaydi: har doim faqat qoralama (PeerSecretaryReason).

const (
	// loopWindow — shu oyna ichidagi avtomatik javoblar sanaladi.
	loopWindow = time.Hour
	// loopMaxAuto — egasi aralashmasdan bitta chatga ketma-ket avtomatik javoblar chegarasi.
	loopMaxAuto = 5
	// loopNotifyEvery — bitta chat bo'yicha sikl ogohlantirishi shundan tez-tez yuborilmaydi.
	loopNotifyEvery = time.Hour
)

// PeerSecretaryReason — qarshi tomon ham kotib ulagan foydalanuvchi: faqat qoralama.
const PeerSecretaryReason = "qarshi tomonda ham kotib — faqat qoralama"

// isPeerSecretary — chatdagi kontakt ham shu xizmatda kotib ulagan foydalanuvchimi.
func (p *Pipeline) isPeerSecretary(chatID int64) bool {
	return p.PeerSecretary != nil && p.PeerSecretary(chatID)
}

// autoLoopBlocked — bu chatga avtomatik javob yuborish sikl xavfi tufayli to'xtatilganmi.
func (p *Pipeline) autoLoopBlocked(chatID int64) bool {
	now := p.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.liveAutoSends(chatID, now)) >= loopMaxAuto
}

// liveAutoSends — oyna ichidagi avtomatik javob vaqtlari (eskilari tashlanadi). p.mu ostida.
func (p *Pipeline) liveAutoSends(chatID int64, now time.Time) []time.Time {
	ts := p.autoSends[chatID]
	i := 0
	for i < len(ts) && now.Sub(ts[i]) >= loopWindow {
		i++
	}
	if ts = ts[i:]; len(ts) == 0 {
		delete(p.autoSends, chatID)
		return nil
	}
	p.autoSends[chatID] = ts
	return ts
}

// noteAutoSend — avtomatik javob yuborildi.
func (p *Pipeline) noteAutoSend(chatID int64) {
	now := p.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	ts := p.liveAutoSends(chatID, now)
	if len(ts) >= 2*loopMaxAuto {
		ts = ts[1:]
	}
	p.autoSends[chatID] = append(ts, now)
	if len(p.autoSends) > maxPendingChats { // xotira cheksiz o'smasin
		for id := range p.autoSends {
			p.liveAutoSends(id, now)
		}
	}
}

// resetAutoStreak — egasi chatga o'zi aralashdi (yozdi yoki qoralamani tasdiqladi).
func (p *Pipeline) resetAutoStreak(chatID int64) {
	p.mu.Lock()
	delete(p.autoSends, chatID)
	p.mu.Unlock()
}

// loopStopped — sikl to'xtatildi: auditga yoziladi va egasiga (soatiga bir marta) bildiriladi.
func (p *Pipeline) loopStopped(ctx context.Context, in Incoming) {
	p.Store.Audit(ctx, p.Owner, "loop_guard", fmt.Sprintf("chat=%d", in.ChatID))
	name := strings.TrimSpace(in.Contact.Name)
	if name == "" {
		name = "Kontakt"
	}
	p.notifyOnce(ctx, fmt.Sprintf("loop:%d", in.ChatID), loopNotifyEvery,
		"🔁 "+name+" bilan suhbatda kotib ketma-ket ko'p avtomatik javob yozdi — qarshi tomonda ham "+
			"avtomatik javobchi (bot yoki boshqa kotib) bo'lishi mumkin. Cheksiz yozishma oldini olish uchun "+
			"bu chatda avtomatik yuborish to'xtatildi: keyingi javoblar qoralama bo'ladi. "+
			"O'zingiz yozsangiz yoki qoralamani tasdiqlasangiz, kotib yana avtomatik javob beradi.", true)
}
