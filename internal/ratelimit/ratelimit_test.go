package ratelimit

import (
	"testing"
	"time"
)

func TestBucketBurstThenBlock(t *testing.T) {
	b := NewBucket(3, 0.0001)
	for i := 0; i < 3; i++ {
		if !b.Allow() {
			t.Fatalf("burst ichida %d-chaqiriq ruxsat etilishi kerak", i)
		}
	}
	if b.Allow() {
		t.Fatal("burstdan keyin bloklanishi kerak")
	}
}

func TestBucketRefill(t *testing.T) {
	b := NewBucket(1, 10)
	fake := time.Now()
	b.now = func() time.Time { return fake }
	b.last = fake
	if !b.Allow() {
		t.Fatal("birinchisi ruxsat")
	}
	if b.Allow() {
		t.Fatal("ikkinchisi bloklanishi kerak")
	}
	fake = fake.Add(200 * time.Millisecond) // 2 token
	if !b.Allow() {
		t.Fatal("to'lgandan keyin ruxsat")
	}
}

func TestLimiterDailyCap(t *testing.T) {
	l := NewLimiter(Config{GlobalBurst: 100, GlobalPerSec: 100, ChatBurst: 100, ChatPerSec: 100, DailyMax: 2})
	if !l.Allow(1) || !l.Allow(2) {
		t.Fatal("birinchi ikkitasi ruxsat")
	}
	if l.Allow(3) {
		t.Fatal("kunlik limitdan keyin bloklanishi kerak")
	}
}

func TestLimiterPerChatIsolation(t *testing.T) {
	l := NewLimiter(Config{GlobalBurst: 100, GlobalPerSec: 100, ChatBurst: 1, ChatPerSec: 0.0001, DailyMax: 100})
	if !l.Allow(1) {
		t.Fatal("chat 1 birinchi")
	}
	if l.Allow(1) {
		t.Fatal("chat 1 ikkinchi bloklanishi kerak")
	}
	if !l.Allow(2) {
		t.Fatal("chat 2 mustaqil bo'lishi kerak")
	}
}

func TestHumanDelayBounds(t *testing.T) {
	for _, r := range []float64{0, 0.5, 0.999} {
		rd, td := humanDelay("salom", string(make([]rune, 5000)), func() float64 { return r })
		if rd > 8*time.Second || td > 17*time.Second || td < time.Second {
			t.Fatalf("chegaradan chiqdi: %v %v", rd, td)
		}
	}
}
