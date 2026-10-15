package store

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kengaytirilgan statistika (Pro): audit hodisalaridan (xabar matnisiz) hisoblanadi —
// kunlik dinamika, tasdiqlash ulushi, faol soatlar, ko'p yozgan kontaktlar va o'tgan davr bilan solishtirish.

// StatsEvents — statistika uchun o'qiladigan hodisalar.
var StatsEvents = []string{"draft_created", "auto_sent", "draft_sent", "draft_rejected", "skip", "blocked"}

type DayStat struct {
	Day      string `json:"day"` // YYYY-MM-DD (egasining vaqt zonasida)
	Prepared int    `json:"prepared"`
	Sent     int    `json:"sent"`
}

type ChatStat struct {
	ChatID int64  `json:"chat_id"`
	Name   string `json:"name"`
	Count  int    `json:"count"`
}

type ExtStats struct {
	Days      int       `json:"days"`
	Series    []DayStat `json:"series"`
	Prepared  int       `json:"prepared"`   // tayyorlangan javoblar (qoralama + avtomatik)
	Sent      int       `json:"sent"`       // yuborilgan (avtomatik + tasdiqlangan qoralama)
	AutoSent  int       `json:"auto_sent"`  //
	DraftSent int       `json:"draft_sent"` //
	Rejected  int       `json:"rejected"`   // rad etilgan qoralamalar
	Skipped   int       `json:"skipped"`    // javobsiz qoldirilgan (qoida, tinch soat, limit...)
	Blocked   int       `json:"blocked"`    // xavfsizlik filtri
	// Approval — tasdiqlangan qoralamalar ulushi, foiz; qoralama hal qilinmagan bo'lsa -1.
	Approval int        `json:"approval"`
	SavedMin int        `json:"saved_min"`
	Hours    [24]int    `json:"hours"` // tayyorlangan javoblar soat bo'yicha (kontaktlar qachon yozadi)
	TopChats []ChatStat `json:"top_chats"`
	// O'tgan shuncha kunlik davr — solishtirish uchun.
	PrevPrepared int `json:"prev_prepared"`
	PrevSent     int `json:"prev_sent"`
}

// statsRowLimit — xotira/vaqt chegarasi (90 kunlik audit; o'rtacha mijozga yetarli).
const statsRowLimit = 50000

// ChatFromDetail — audit detail'idagi "chat=<id>" (yo'q bo'lsa 0).
func ChatFromDetail(detail string) int64 {
	for _, f := range strings.Fields(detail) {
		if v, ok := strings.CutPrefix(f, "chat="); ok {
			id, _ := strconv.ParseInt(v, 10, 64)
			return id
		}
	}
	return 0
}

// ExtendedStats oxirgi days kunlik (bugun ham) statistika, loc bo'yicha.
func ExtendedStats(ctx context.Context, st Store, owner int64, now time.Time, days int, loc *time.Location) (ExtStats, error) {
	if days < 1 {
		days = 1
	}
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	start := today.AddDate(0, 0, -(days - 1))
	prevStart := start.AddDate(0, 0, -days)
	rows, err := st.ListAuditSince(ctx, owner, StatsEvents, prevStart, statsRowLimit)
	if err != nil {
		return ExtStats{}, err
	}
	out := ExtStats{Days: days, Series: make([]DayStat, days), Approval: -1}
	index := map[string]int{}
	for i := range out.Series {
		d := start.AddDate(0, 0, i).Format(time.DateOnly)
		out.Series[i].Day, index[d] = d, i
	}
	chats := map[int64]int{}
	for _, e := range rows {
		at := e.At.In(loc)
		prepared := e.Event == "draft_created" || e.Event == "auto_sent"
		sent := e.Event == "auto_sent" || e.Event == "draft_sent"
		if at.Before(start) {
			if prepared {
				out.PrevPrepared++
			}
			if sent {
				out.PrevSent++
			}
			continue
		}
		i, ok := index[at.Format(time.DateOnly)]
		if !ok {
			continue // kelajak (soat farqi) — e'tiborsiz
		}
		switch e.Event {
		case "auto_sent":
			out.AutoSent++
		case "draft_sent":
			out.DraftSent++
		case "draft_rejected":
			out.Rejected++
		case "skip":
			out.Skipped++
		case "blocked":
			out.Blocked++
		}
		if prepared {
			out.Prepared++
			out.Series[i].Prepared++
			out.Hours[at.Hour()]++
			if c := ChatFromDetail(e.Detail); c != 0 {
				chats[c]++
			}
		}
		if sent {
			out.Sent++
			out.Series[i].Sent++
		}
	}
	if n := out.DraftSent + out.Rejected; n > 0 {
		out.Approval = (out.DraftSent*100 + n/2) / n
	}
	out.SavedMin = int(time.Duration(out.Sent) * SavedPerReply / time.Minute)
	for id, n := range chats {
		out.TopChats = append(out.TopChats, ChatStat{ChatID: id, Count: n})
	}
	sort.Slice(out.TopChats, func(i, j int) bool {
		if out.TopChats[i].Count != out.TopChats[j].Count {
			return out.TopChats[i].Count > out.TopChats[j].Count
		}
		return out.TopChats[i].ChatID < out.TopChats[j].ChatID
	})
	if len(out.TopChats) > 5 {
		out.TopChats = out.TopChats[:5]
	}
	return out, nil
}

// BusiestHour — eng ko'p javob tayyorlangan soat (-1 — ma'lumot yo'q).
func (s ExtStats) BusiestHour() int {
	best, h := 0, -1
	for i, n := range s.Hours {
		if n > best {
			best, h = n, i
		}
	}
	return h
}
