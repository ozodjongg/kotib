// Package ratelimit: ban xavfini kamaytiruvchi tezlik cheklovlari va
// "odamga o'xshash" kechikishlar.
package ratelimit

import (
	"math/rand"
	"sync"
	"time"
	"unicode/utf8"
)

// Bucket — oddiy token-bucket. Zero value ishlamaydi, NewBucket ishlating.
type Bucket struct {
	mu       sync.Mutex
	capacity float64
	tokens   float64
	rate     float64 // token/sekund
	last     time.Time
	now      func() time.Time
}

func NewBucket(capacity int, perSecond float64) *Bucket {
	return &Bucket{
		capacity: float64(capacity),
		tokens:   float64(capacity),
		rate:     perSecond,
		last:     time.Now(),
		now:      time.Now,
	}
}

// Allow bitta token olishga urinadi.
func (b *Bucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.now()
	b.tokens += t.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = t
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// full bucket to'liq tiklanganmi (uni o'chirish yangisini yaratish bilan teng).
func (b *Bucket) full() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tokens+b.now().Sub(b.last).Seconds()*b.rate >= b.capacity
}

const maxChatBuckets = 1000

// Limiter — mijoz akkaunti uchun uch qavatli cheklov:
// global (akkaunt bo'yicha), har chat bo'yicha, va kunlik umumiy limit.
type Limiter struct {
	mu        sync.Mutex
	global    *Bucket
	perChat   map[int64]*Bucket
	chatCap   int
	chatRate  float64
	dailyMax  int
	dailyUsed int
	dayStart  time.Time
}

type Config struct {
	GlobalBurst  int     // masalan 5
	GlobalPerSec float64 // masalan 0.2  (~12/daqiqa)
	ChatBurst    int     // masalan 3
	ChatPerSec   float64 // masalan 0.05 (~3/daqiqa)
	DailyMax     int     // masalan 300
}

func DefaultConfig() Config {
	return Config{GlobalBurst: 5, GlobalPerSec: 0.2, ChatBurst: 3, ChatPerSec: 0.05, DailyMax: 300}
}

func NewLimiter(c Config) *Limiter {
	return &Limiter{
		global:   NewBucket(c.GlobalBurst, c.GlobalPerSec),
		perChat:  map[int64]*Bucket{},
		chatCap:  c.ChatBurst,
		chatRate: c.ChatPerSec,
		dailyMax: c.DailyMax,
		dayStart: time.Now().Truncate(24 * time.Hour),
	}
}

// Allow chatID uchun yuborishga ruxsat bor-yo'qligini aytadi.
// Ruxsat berilsa, hisoblagichlar yangilanadi.
func (l *Limiter) Allow(chatID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if now := time.Now(); now.Sub(l.dayStart) >= 24*time.Hour {
		l.dayStart = now.Truncate(24 * time.Hour)
		l.dailyUsed = 0
	}
	if l.dailyUsed >= l.dailyMax {
		return false
	}
	cb, ok := l.perChat[chatID]
	if !ok {
		if len(l.perChat) >= maxChatBuckets {
			// Xotira cheksiz o'smasin: to'lgan (kutayotgan bo'lmagan) bucket'lar ahamiyatsiz.
			for id, b := range l.perChat {
				if b.full() {
					delete(l.perChat, id)
				}
			}
		}
		cb = NewBucket(l.chatCap, l.chatRate)
		l.perChat[chatID] = cb
	}
	// avval chat, keyin global: global tokenni behuda sarflamaslik uchun
	if !cb.Allow() {
		return false
	}
	if !l.global.Allow() {
		return false
	}
	l.dailyUsed++
	return true
}

// HumanDelay xabar uzunligiga mos "o'qish + yozish" kechikishini hisoblaydi:
// avval kiruvchi xabarni "o'qish" (1.5–5s), keyin yozish (~7 belgi/s) — jami
// 2s dan 25s gacha, tasodifiy jitter bilan.
func HumanDelay(incoming, reply string) (readDelay, typingDelay time.Duration) {
	return humanDelay(incoming, reply, rand.Float64)
}

func humanDelay(incoming, reply string, rnd func() float64) (time.Duration, time.Duration) {
	read := 1500*time.Millisecond + time.Duration(rnd()*3500)*time.Millisecond
	read += time.Duration(utf8.RuneCountInString(incoming)) * 15 * time.Millisecond

	chars := utf8.RuneCountInString(reply)
	typing := time.Duration(float64(chars) / 7.0 * float64(time.Second)) // ~7 belgi/s
	typing = time.Duration(float64(typing) * (0.8 + rnd()*0.5))          // 0.8x–1.3x

	if read > 8*time.Second {
		read = 8 * time.Second
	}
	if typing < time.Second {
		typing = time.Second
	}
	if typing > 17*time.Second {
		typing = 17 * time.Second
	}
	return read, typing
}
