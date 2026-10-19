package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Sport — o'yin natijalari, taqvim va turnir jadvali: ESPN ochiq API (kalitsiz). Mijoz
// sevimli ligasi va jamoasini tanlaydi; AI kunlik/haftalik xulosa tayyorlaydi.
type Sport struct {
	HTTP    *http.Client
	BaseURL string // default https://site.api.espn.com/apis
}

// sportLeagues — qo'llanadigan ligalar (qiymat — ESPN yo'li "sport/liga"; ixtiyoriy yo'l yo'q).
var sportLeagues = []Option{
	{"soccer/eng.1", "⚽ Angliya Premer-ligasi"},
	{"soccer/esp.1", "⚽ Ispaniya La Liga"},
	{"soccer/ita.1", "⚽ Italiya Seriya A"},
	{"soccer/ger.1", "⚽ Germaniya Bundesliga"},
	{"soccer/fra.1", "⚽ Fransiya Liga 1"},
	{"soccer/rus.1", "⚽ Rossiya Premer-ligasi"},
	{"soccer/tur.1", "⚽ Turkiya Superligasi"},
	{"soccer/uefa.champions", "⚽ UEFA Chempionlar ligasi"},
	{"soccer/uefa.europa", "⚽ UEFA Evropa ligasi"},
	{"soccer/fifa.world", "⚽ Jahon chempionati"},
	{"basketball/nba", "🏀 NBA"},
	{"hockey/nhl", "🏒 NHL"},
}

func sportLeague(v string) (string, bool) {
	for _, o := range sportLeagues {
		if o.Value == v {
			return o.Label, true
		}
	}
	return "", false
}

func (s *Sport) Info() Info {
	return Info{ID: "sport", Name: "Sport natijalari", Icon: "⚽", Category: "Kundalik",
		Description: "Sevimli ligangiz va jamoangiz: o'yin natijalari, yaqin o'yinlar taqvimi va turnir jadvali (ESPN ma'lumotlari, kalit kerak emas).",
		Fields: []Field{
			{Key: "league", Label: "Asosiy liga", Type: FieldSelect, Default: "soccer/eng.1", Options: sportLeagues},
			{Key: "team", Label: "Sevimli jamoa (ixtiyoriy)", Type: FieldText, Placeholder: "Real Madrid",
				Help: "Jamoa nomini lotin harflarida, inglizcha yozing (masalan: Barcelona, Manchester United)."},
		},
		Examples: []string{"Har dushanba 09:00 da sevimli jamoamning o'tgan hafta natijalari va jadvaldagi o'rni", "Har kuni 18:00 da bugungi o'yinlar va boshlanish vaqtlari"}}
}

func (s *Sport) Tools() []llm.ToolDecl {
	leagues := make([]string, len(sportLeagues))
	for i, o := range sportLeagues {
		leagues[i] = o.Value
	}
	league := map[string]any{"type": "STRING", "enum": leagues, "description": "Liga (ixtiyoriy, standart — egasi tanlagani)"}
	return []llm.ToolDecl{
		{Name: "sport_scores", Description: "O'yinlar: natijalar (o'tgan kunlar) yoki taqvim (kelgusi kunlar), Toshkent vaqtida. Jamoa bo'yicha filtrlash mumkin.",
			Params: obj(map[string]any{
				"league": league,
				"from":   map[string]any{"type": "INTEGER", "description": "Bugundan necha kun oldin/keyin boshlab: -7…7 (masalan -1 — kechadan), standart 0"},
				"days":   map[string]any{"type": "INTEGER", "description": "Necha kunlik oraliq, 1–7, standart 1"},
				"team":   str("Jamoa nomi (ixtiyoriy; bo'sh — egasining sevimli jamoasi, «all» — barcha o'yinlar)"),
			})},
		{Name: "sport_standings", Description: "Liga turnir jadvali: o'rin, o'yinlar, g'alaba/durang/mag'lubiyat, ochko.",
			Params: obj(map[string]any{"league": league})},
	}
}

func (s *Sport) base() string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}
	return "https://site.api.espn.com/apis"
}

func (s *Sport) get(ctx context.Context, path string, q url.Values, out any) error {
	u := s.base() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	data, err := getBody(ctx, s.HTTP, u, nil)
	if err != nil {
		return errors.New("sport ma'lumotlarini olib bo'lmadi, keyinroq urinib ko'ring")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("sport ma'lumotlarini o'qib bo'lmadi")
	}
	return nil
}

// league — vosita argumenti yoki sozlamadagi liga (faqat ro'yxatdagilar).
func (s *Sport) league(cfg Config, args map[string]any) (string, string, error) {
	l := argString(args, "league")
	if l == "" {
		l = cfg["league"]
	}
	if l == "" {
		l = "soccer/eng.1"
	}
	name, ok := sportLeague(l)
	if !ok {
		return "", "", errors.New("bu liga qo'llanmaydi")
	}
	return l, name, nil
}

func (s *Sport) Call(ctx context.Context, cfg Config, tool string, args map[string]any) (string, error) {
	league, name, err := s.league(cfg, args)
	if err != nil {
		return "", err
	}
	if tool == "sport_standings" {
		return s.standings(ctx, league, name)
	}
	return s.scores(ctx, cfg, league, name, args)
}

type espnCompetitor struct {
	HomeAway string `json:"homeAway"`
	Score    string `json:"score"`
	Winner   bool   `json:"winner"`
	Team     struct {
		DisplayName      string `json:"displayName"`
		ShortDisplayName string `json:"shortDisplayName"`
	} `json:"team"`
}

func (c espnCompetitor) matches(team string) bool {
	return strings.Contains(strings.ToLower(c.Team.DisplayName), team) ||
		strings.Contains(strings.ToLower(c.Team.ShortDisplayName), team)
}

func (s *Sport) scores(ctx context.Context, cfg Config, league, name string, args map[string]any) (string, error) {
	team := argString(args, "team")
	if team == "" {
		team = cfg["team"]
	}
	if strings.EqualFold(team, "all") || utf8.RuneCountInString(team) > 60 {
		team = ""
	}
	team = strings.ToLower(team)
	from := argInt(args, "from", 0, -7, 7)
	days := argInt(args, "days", 1, 1, 7)
	now := time.Now().In(uzLoc)
	start := now.AddDate(0, 0, from)
	end := start.AddDate(0, 0, days-1)
	dates := start.Format("20060102")
	if days > 1 {
		dates += "-" + end.Format("20060102")
	}
	var out struct {
		Events []struct {
			Date         string `json:"date"`
			Competitions []struct {
				Competitors []espnCompetitor `json:"competitors"`
				Status      struct {
					Type struct {
						State       string `json:"state"`
						ShortDetail string `json:"shortDetail"`
					} `json:"type"`
				} `json:"status"`
			} `json:"competitions"`
		} `json:"events"`
	}
	if err := s.get(ctx, "/site/v2/sports/"+league+"/scoreboard", url.Values{"dates": {dates}, "limit": {"200"}}, &out); err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for _, e := range out.Events {
		if len(e.Competitions) == 0 {
			continue
		}
		c := e.Competitions[0]
		var home, away espnCompetitor
		for _, t := range c.Competitors {
			if t.HomeAway == "home" {
				home = t
			} else {
				away = t
			}
		}
		if team != "" && !home.matches(team) && !away.matches(team) {
			continue
		}
		when := e.Date
		if t, err := time.Parse("2006-01-02T15:04Z07:00", e.Date); err == nil {
			when = t.In(uzLoc).Format("02.01 15:04")
		}
		var line string
		switch c.Status.Type.State {
		case "pre":
			line = fmt.Sprintf("%s — %s : %s (boshlanadi)", when, home.Team.DisplayName, away.Team.DisplayName)
		case "in":
			line = fmt.Sprintf("%s — %s %s:%s %s (jonli: %s)", when, home.Team.DisplayName, home.Score, away.Score, away.Team.DisplayName, c.Status.Type.ShortDetail)
		default:
			line = fmt.Sprintf("%s — %s %s:%s %s (%s)", when, home.Team.DisplayName, home.Score, away.Score, away.Team.DisplayName, c.Status.Type.ShortDetail)
		}
		b.WriteString(line + "\n")
		n++
		if b.Len() > maxFeedOutput {
			b.WriteString("…(qisqartirildi)")
			break
		}
	}
	period := start.Format("02.01.2006")
	if days > 1 {
		period += "–" + end.Format("02.01.2006")
	}
	if n == 0 {
		if team != "" {
			return fmt.Sprintf("%s, %s: «%s» jamoasining o'yini yo'q.", name, period, team), nil
		}
		return fmt.Sprintf("%s, %s: o'yinlar yo'q.", name, period), nil
	}
	return fmt.Sprintf("%s, %s (Toshkent vaqti), %d ta o'yin:\n%s", name, period, n, b.String()), nil
}

func (s *Sport) standings(ctx context.Context, league, name string) (string, error) {
	type entry struct {
		Team struct {
			DisplayName string `json:"displayName"`
		} `json:"team"`
		Stats []struct {
			Name  string  `json:"name"`
			Value float64 `json:"value"`
		} `json:"stats"`
	}
	type group struct {
		Name      string `json:"name"`
		Standings struct {
			Entries []entry `json:"entries"`
		} `json:"standings"`
	}
	var out struct {
		group
		Children []group `json:"children"`
	}
	if err := s.get(ctx, "/v2/sports/"+league+"/standings", nil, &out); err != nil {
		return "", err
	}
	groups := out.Children
	if len(groups) == 0 && len(out.Standings.Entries) > 0 {
		groups = []group{out.group}
	}
	var b strings.Builder
	for _, g := range groups {
		type row struct {
			team string
			st   map[string]float64
		}
		rows := make([]row, 0, len(g.Standings.Entries))
		for _, e := range g.Standings.Entries {
			st := map[string]float64{}
			for _, x := range e.Stats {
				st[x.Name] = x.Value
			}
			rows = append(rows, row{e.Team.DisplayName, st})
		}
		sort.SliceStable(rows, func(i, j int) bool {
			a, c := rows[i].st, rows[j].st
			if a["rank"] > 0 && c["rank"] > 0 {
				return a["rank"] < c["rank"]
			}
			if a["points"] != c["points"] {
				return a["points"] > c["points"]
			}
			return a["wins"] > c["wins"]
		})
		if len(groups) > 1 && g.Name != "" {
			b.WriteString("### " + g.Name + "\n")
		}
		for pos, r := range rows {
			if _, ok := r.st["points"]; ok {
				fmt.Fprintf(&b, "%d. %s — %.0f o'yin, %.0f-%.0f-%.0f, %.0f ochko\n", pos+1, r.team,
					r.st["gamesPlayed"], r.st["wins"], r.st["ties"], r.st["losses"], r.st["points"])
			} else {
				fmt.Fprintf(&b, "%d. %s — %.0f g'alaba, %.0f mag'lubiyat\n", pos+1, r.team, r.st["wins"], r.st["losses"])
			}
		}
		if b.Len() > maxFeedOutput {
			b.WriteString("…(qisqartirildi)")
			break
		}
	}
	if b.Len() == 0 {
		return name + ": jadval hozircha yo'q (mavsum boshlanmagan bo'lishi mumkin).", nil
	}
	return name + " — turnir jadvali (o'yin, G-D-M, ochko):\n" + b.String(), nil
}
