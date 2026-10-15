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

func TestAwayExpiry(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s := Default()
	s.SetAway("  Dars   qilyapman ", 90, now)
	if s.Away != "Dars qilyapman" || s.AwayUntil == nil || !s.AwayUntil.Equal(now.Add(90*time.Minute)) {
		t.Fatalf("%q %v", s.Away, s.AwayUntil)
	}
	if s.CurrentAway(now.Add(89*time.Minute)) == "" || s.CurrentAway(now.Add(90*time.Minute)) != "" {
		t.Fatal("muddat tugagach holat bo'sh bo'lishi kerak")
	}
	s.SetAway("Safardaman", 0, now) // muddatsiz
	if s.AwayUntil != nil || s.CurrentAway(now.Add(1000*time.Hour)) != "Safardaman" {
		t.Fatal("muddatsiz holat")
	}
	s.SetAway("Uxlayapman", 99999, now) // 1 kun bilan cheklanadi
	if !s.AwayUntil.Equal(now.Add(MaxAwayMinutes * time.Minute)) {
		t.Fatal(s.AwayUntil)
	}
	s.SetAway("", 60, now)
	if s.Away != "" || s.AwayUntil != nil {
		t.Fatal("tozalash")
	}
}

func TestPersonaMine(t *testing.T) {
	s := Default()
	s.DefaultPersona = PersonaMine
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	c := Contact{UserID: 1, IsContact: true}
	// Profil hali yo'q — neytral uslub.
	if d := s.Resolve(c, noon()); d.Persona != Presets["neutral"] {
		t.Fatalf("profilsiz: %q", d.Persona)
	}
	s.StyleProfile = "Juda qisqa yozadi, \"xop\" deydi."
	if d := s.Resolve(c, noon()); !strings.Contains(d.Persona, "xop") {
		t.Fatalf("profil: %q", d.Persona)
	}
	s.Rules = []Rule{{ID: "r", Match: Match{UserIDs: []int64{2}}, Persona: PersonaMine}}
	if err := s.Validate(); err != nil {
		t.Fatalf("qoidada mine: %v", err)
	}
}

func TestValidateNewFields(t *testing.T) {
	ok := Default()
	ok.ContactNotes = map[int64]string{5: "  onam  ", 6: "   "}
	ok.Knowledge = strings.Repeat("a", MaxKnowledge)
	future := time.Now().AddDate(0, 0, 7)
	ok.ShadowUntil = &future
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok.ContactNotes[5] != "onam" {
		t.Fatalf("eslatma tozalanmadi: %q", ok.ContactNotes[5])
	}
	if _, has := ok.ContactNotes[6]; has {
		t.Fatal("bo'sh eslatma o'chirilishi kerak")
	}
	far := time.Now().AddDate(0, 1, 0)
	bad := []func(*Settings){
		func(s *Settings) { s.Knowledge = strings.Repeat("a", MaxKnowledge+1) },
		func(s *Settings) { s.StyleProfile = strings.Repeat("a", MaxStyleProfile+1) },
		func(s *Settings) { s.ContactNotes = map[int64]string{-1: "x"} },
		func(s *Settings) { s.ContactNotes = map[int64]string{1: strings.Repeat("a", MaxContactNote+1)} },
		func(s *Settings) { s.UnansweredHours = 49 },
		func(s *Settings) { s.StyleScore = 101 },
		func(s *Settings) { s.ShadowUntil = &far },
		func(s *Settings) { s.DefaultPersona = "boshqa" },
	}
	for i, f := range bad {
		s := Default()
		f(&s)
		if s.Validate() == nil {
			t.Errorf("#%d: xato kutilgan edi", i)
		}
	}
}

func TestShadowAndUnanswered(t *testing.T) {
	s := Default()
	now := noon()
	if s.ShadowActive(now) {
		t.Fatal("default soya rejimi o'chiq")
	}
	until := now.Add(time.Hour)
	s.ShadowUntil = &until
	if !s.ShadowActive(now) || s.ShadowActive(until) {
		t.Fatal("soya rejimi muddati")
	}
	if s.Unanswered() != 4*time.Hour {
		t.Fatalf("default: %v", s.Unanswered())
	}
	s.UnansweredHours = 2
	if s.Unanswered() != 2*time.Hour {
		t.Fatal("2 soat")
	}
	s.NoUnanswered = true
	if s.Unanswered() != 0 {
		t.Fatal("o'chiq")
	}
}

func TestQuietReason(t *testing.T) {
	s := Default()
	loc, _ := time.LoadLocation("Asia/Tashkent")
	d := s.Resolve(Contact{UserID: 1, IsContact: true}, time.Date(2026, 10, 13, 23, 30, 0, 0, loc))
	if d.Reply || d.Reason != ReasonQuiet {
		t.Fatalf("%+v", d)
	}
}
