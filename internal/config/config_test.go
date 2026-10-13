package config

import (
	"strings"
	"testing"
	"time"
)

func noon() time.Time {
	loc, _ := time.LoadLocation("Asia/Tashkent")
	return time.Date(2026, 10, 13, 12, 0, 0, 0, loc)
}

func TestUnknownContactsSkippedByDefault(t *testing.T) {
	s := Default()
	d := s.Resolve(Contact{UserID: 1, IsContact: false}, noon())
	if d.Reply {
		t.Fatal("kontaktda yo'qlarga default javob berilmasligi kerak")
	}
}

func TestGroupsAndBotsSkipped(t *testing.T) {
	s := Default()
	if s.Resolve(Contact{UserID: 1, IsContact: true, IsGroup: true}, noon()).Reply {
		t.Fatal("guruh")
	}
	if s.Resolve(Contact{UserID: 1, IsContact: true, IsBot: true}, noon()).Reply {
		t.Fatal("bot")
	}
}

func TestQuietHoursWrapMidnight(t *testing.T) {
	s := Default()
	loc, _ := time.LoadLocation("Asia/Tashkent")
	c := Contact{UserID: 1, IsContact: true}
	if s.Resolve(c, time.Date(2026, 10, 13, 23, 30, 0, 0, loc)).Reply {
		t.Fatal("23:30 tinch soat")
	}
	if s.Resolve(c, time.Date(2026, 10, 13, 3, 0, 0, 0, loc)).Reply {
		t.Fatal("03:00 tinch soat")
	}
	if !s.Resolve(c, noon()).Reply {
		t.Fatal("12:00 javob berilishi kerak")
	}
}

func TestRuleByTagOverridesPersona(t *testing.T) {
	s := Default()
	s.ContactTags = map[int64][]string{7: {"do'st"}}
	s.Rules = []Rule{{ID: "r1", Match: Match{Tags: []string{"Do'st"}}, Persona: "joke"}}
	d := s.Resolve(Contact{UserID: 7, IsContact: true}, noon())
	if !d.Reply || d.RuleID != "r1" || d.Persona != Presets["joke"] {
		t.Fatalf("qoida ishlamadi: %+v", d)
	}
	d = s.Resolve(Contact{UserID: 8, IsContact: true}, noon())
	if d.RuleID != "" || d.Persona != Presets["neutral"] {
		t.Fatalf("boshqa kontaktga qoida tegmasligi kerak: %+v", d)
	}
}

func TestRuleCannotEscalateDraftToAuto(t *testing.T) {
	s := Default() // draft
	auto := ModeAuto
	s.Rules = []Rule{{ID: "r1", Match: Match{UserIDs: []int64{5}}, Mode: &auto}}
	d := s.Resolve(Contact{UserID: 5, IsContact: true}, noon())
	if d.Mode != ModeDraft {
		t.Fatalf("draft→auto ko'tarilmasligi kerak: %v", d.Mode)
	}
}

func TestEmptyMatchNeverMatches(t *testing.T) {
	s := Default()
	s.Rules = []Rule{{ID: "r1", Match: Match{}, Persona: "joke"}}
	d := s.Resolve(Contact{UserID: 1, IsContact: true}, noon())
	if d.RuleID != "" {
		t.Fatal("bo'sh match hammaga mos kelmasligi kerak")
	}
}

func TestValidate(t *testing.T) {
	s := Default()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.Mode = "hack"
	if s.Validate() == nil {
		t.Fatal("noto'g'ri mode rad etilishi kerak")
	}
	s = Default()
	s.Rules = []Rule{{ID: "a"}, {ID: "a"}}
	if s.Validate() == nil {
		t.Fatal("takror id rad etilishi kerak")
	}
	s = Default()
	s.Quiet.TZ = "Mars/Olympus"
	if s.Validate() == nil {
		t.Fatal("noto'g'ri tz rad etilishi kerak")
	}
}

func TestValidateLimits(t *testing.T) {
	long := strings.Repeat("a", 200)
	s := Default()
	s.Rules = []Rule{{ID: "r1", Title: long}}
	if s.Validate() == nil {
		t.Fatal("uzun nom rad etilishi kerak")
	}
	s = Default()
	s.Rules = []Rule{{ID: "r1", Match: Match{UserIDs: []int64{-5}}}}
	if s.Validate() == nil {
		t.Fatal("manfiy id rad etilishi kerak")
	}
	s = Default()
	s.Rules = []Rule{{ID: "r1", Match: Match{Tags: []string{"  Oila "}}}}
	if err := s.Validate(); err != nil || s.Rules[0].Match.Tags[0] != "oila" {
		t.Fatalf("teg normallashtirilishi kerak: %v %v", err, s.Rules[0].Match.Tags)
	}
	s = Default()
	s.Rules = nil
	if err := s.Validate(); err != nil || s.Rules == nil {
		t.Fatal("rules nil bo'lmasligi kerak")
	}
}

func TestTelegramServiceNeverAnswered(t *testing.T) {
	s := Default()
	s.ReplyUnknown = true // hatto "hammaga javob" yoqilgan bo'lsa ham
	s.Quiet.Enabled = false
	for _, id := range []int64{777000, 42777} {
		if d := s.Resolve(Contact{UserID: id, IsContact: true}, noon()); d.Reply {
			t.Fatalf("%d: Telegram xizmati xabariga javob bo'lmasligi kerak", id)
		}
	}
	if d := s.Resolve(Contact{UserID: 5, IsBot: true}, noon()); d.Reply {
		t.Fatal("botga javob bo'lmasligi kerak")
	}
}

func TestReplyBotsSetting(t *testing.T) {
	s := Default()
	s.Quiet.Enabled = false
	s.Mode = ModeAuto
	bot := Contact{UserID: 9, IsBot: true}
	if s.ReplyBots {
		t.Fatal("default false bo'lishi kerak")
	}
	if d := s.Resolve(bot, noon()); d.Reply {
		t.Fatal("o'chiq bo'lsa botga javob yo'q")
	}
	s.ReplyBots = true
	d := s.Resolve(bot, noon())
	if !d.Reply || d.Mode != ModeDraft {
		t.Fatalf("yoqilsa javob bo'ladi, lekin faqat draft: %+v", d)
	}
	if d := s.Resolve(Contact{UserID: 777000, IsBot: true}, noon()); d.Reply {
		t.Fatal("Telegram xizmati baribir bloklangan")
	}
	// oddiy kontaktga avto-rejim o'zgarmaydi
	if d := s.Resolve(Contact{UserID: 10, IsContact: true}, noon()); d.Mode != ModeAuto {
		t.Fatalf("kontakt: %+v", d)
	}
}
