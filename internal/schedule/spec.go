// Package schedule: rejalashtirilgan AI vazifalar (Claude'dagi Scheduled kabi). Egasi vaqt
// (bir martalik, har soat, har kuni, ish kunlari, haftaning tanlangan kunlari) va AI'ga
// ko'rsatma yozadi; kotib o'sha vaqtda ko'rsatmani yoqilgan plaginlar bilan bajarib, natijani
// bot chatiga yuboradi.
package schedule

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

const (
	MaxPerOwner   = 20   // egasida ko'pi bilan shuncha vazifa
	MaxRunsPerDay = 60   // egasida kuniga ko'pi bilan shuncha bajarilish (AI xarajati)
	MaxTitle      = 80   // belgi
	MaxPrompt     = 2000 // belgi
	MaxResult     = 3500 // saqlanadigan natija (bot xabari chegarasi)
)

var (
	hmRe   = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ErrInvalid — kiritish xatosi (matni mijozga ko'rsatiladi).
type ErrInvalid struct{ Msg string }

func (e *ErrInvalid) Error() string { return e.Msg }

func invalid(msg string) error { return &ErrInvalid{msg} }

// Normalize mijoz kiritgan vazifani tekshiradi va tozalaydi (nom bo'sh bo'lsa promptdan olinadi).
func Normalize(s *store.Schedule) error {
	s.Prompt = strings.TrimSpace(s.Prompt)
	s.Title = strings.Join(strings.Fields(s.Title), " ")
	if s.Prompt == "" {
		return invalid("AI uchun ko'rsatma (prompt) yozing")
	}
	if utf8.RuneCountInString(s.Prompt) > MaxPrompt {
		return invalid("ko'rsatma " + strconv.Itoa(MaxPrompt) + " belgidan oshmasin")
	}
	if strings.ContainsRune(s.Prompt, 0) || strings.ContainsRune(s.Title, 0) {
		return invalid("matn noto'g'ri")
	}
	if s.Title == "" {
		s.Title = strings.Join(strings.Fields(s.Prompt), " ")
	}
	if utf8.RuneCountInString(s.Title) > MaxTitle {
		s.Title = string([]rune(s.Title)[:MaxTitle-1]) + "…"
	}
	if !hmRe.MatchString(s.At) {
		return invalid("vaqt SS:DD formatida bo'lsin (masalan 09:00)")
	}
	switch s.Kind {
	case store.ScheduleOnce:
		if !dateRe.MatchString(s.Date) {
			return invalid("sanani tanlang")
		}
		if _, err := time.Parse("2006-01-02", s.Date); err != nil {
			return invalid("sana noto'g'ri")
		}
		s.Weekdays = 0
	case store.ScheduleWeekly:
		if s.Weekdays <= 0 || s.Weekdays > 127 {
			return invalid("kamida bitta kunni tanlang")
		}
		s.Date = ""
	case store.ScheduleHourly, store.ScheduleDaily, store.ScheduleWeekdays:
		s.Date, s.Weekdays = "", 0
	default:
		return invalid("takrorlanish turi noto'g'ri")
	}
	return nil
}

// Next — after'dan keyingi bajarilish vaqti (loc — egasining mintaqasi); nil — boshqa bajarilmaydi
// (bir martalik vazifaning vaqti o'tgan).
func Next(s store.Schedule, after time.Time, loc *time.Location) *time.Time {
	if loc == nil {
		loc = time.UTC
	}
	h, m, ok := parseHM(s.At)
	if !ok {
		return nil
	}
	local := after.In(loc)
	switch s.Kind {
	case store.ScheduleOnce:
		d, err := time.ParseInLocation("2006-01-02", s.Date, loc)
		if err != nil {
			return nil
		}
		t := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, loc)
		if !t.After(after) {
			return nil
		}
		return &t
	case store.ScheduleHourly:
		t := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), m, 0, 0, loc)
		for !t.After(after) {
			t = t.Add(time.Hour)
		}
		return &t
	}
	for i := 0; i <= 8; i++ {
		t := time.Date(local.Year(), local.Month(), local.Day()+i, h, m, 0, 0, loc)
		if t.After(after) && dayOK(s, t.Weekday()) {
			return &t
		}
	}
	return nil
}

func dayOK(s store.Schedule, wd time.Weekday) bool {
	switch s.Kind {
	case store.ScheduleWeekdays:
		return wd != time.Saturday && wd != time.Sunday
	case store.ScheduleWeekly:
		return s.Weekdays&(1<<wd) != 0
	}
	return true
}

func parseHM(v string) (int, int, bool) {
	if !hmRe.MatchString(v) {
		return 0, 0, false
	}
	h, _ := strconv.Atoi(v[:2])
	m, _ := strconv.Atoi(v[3:])
	return h, m, true
}

var dayNames = [...]string{"Ya", "Du", "Se", "Ch", "Pa", "Ju", "Sh"}

// Describe — jadvalning qisqa tavsifi (bot xabari va ro'yxat uchun).
func Describe(s store.Schedule) string {
	switch s.Kind {
	case store.ScheduleOnce:
		return s.Date + " " + s.At + " (bir marta)"
	case store.ScheduleHourly:
		return "har soat :" + s.At[3:]
	case store.ScheduleDaily:
		return "har kuni " + s.At
	case store.ScheduleWeekdays:
		return "ish kunlari (Du–Ju) " + s.At
	case store.ScheduleWeekly:
		var ds []string
		for _, i := range []int{1, 2, 3, 4, 5, 6, 0} {
			if s.Weekdays&(1<<i) != 0 {
				ds = append(ds, dayNames[i])
			}
		}
		return strings.Join(ds, ", ") + " " + s.At
	}
	return ""
}

var errPast = errors.New("bu vaqt allaqachon o'tgan — kelajakdagi sana/vaqtni tanlang")

// Plan — yangi yoki tahrirlangan vazifa uchun NextRun'ni hisoblaydi. Yoqilgan bir martalik
// vazifaning vaqti o'tgan bo'lsa — xato.
func Plan(s *store.Schedule, now time.Time, loc *time.Location) error {
	if !s.Enabled {
		s.NextRun = nil
		return nil
	}
	s.NextRun = Next(*s, now, loc)
	if s.NextRun == nil {
		return &ErrInvalid{errPast.Error()}
	}
	return nil
}
