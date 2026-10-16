package plugin

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// newsSource — RSS lentasi (faqat shu ro'yxatdagi manbalar: ixtiyoriy URL — SSRF xavfi).
type newsSource struct{ ID, Name, URL string }

var newsSources = []newsSource{
	{"kun", "Kun.uz", "https://kun.uz/news/rss"},
	{"gazeta", "Gazeta.uz", "https://www.gazeta.uz/uz/rss/"},
	{"daryo", "Daryo.uz", "https://daryo.uz/feed/"},
	{"uza", "UzA", "https://uza.uz/uz/rss"},
	{"spot", "Spot.uz", "https://www.spot.uz/oz/rss/"},
}

// News — O'zbekiston yangiliklari (RSS, kalitsiz).
type News struct {
	HTTP *http.Client
	// URLs — test uchun manba manzillari (ID → URL); bo'sh — haqiqiy lentalar.
	URLs map[string]string
}

func (n *News) Info() Info {
	var names []string
	for _, s := range newsSources {
		names = append(names, s.ID+" — "+s.Name)
	}
	return Info{ID: "news", Name: "Yangiliklar", Icon: "📰", Category: "Yangiliklar",
		Description: "Kun.uz, Gazeta.uz, Daryo.uz, UzA, Spot.uz lentalaridan so'nggi yangiliklar — AI ularni qisqa xulosa qiladi, havolalari bilan.",
		Fields: []Field{{Key: "sources", Label: "Manbalar", Type: FieldText, Default: "kun,gazeta,daryo",
			Help: "Vergul bilan: " + strings.Join(names, ", ")}},
		Examples: []string{"Har kuni 08:00 da kechagi kunning eng muhim 7 ta yangiligi", "Har juma — haftaning iqtisodiy yangiliklari"}}
}

func (n *News) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "news_feed", Description: "O'zbekiston yangilik saytlarining so'nggi maqolalari (sarlavha, qisqa matn, havola).",
		Params: obj(map[string]any{
			"sources": str("Manbalar vergul bilan: kun, gazeta, daryo, uza, spot (ixtiyoriy)"),
			"hours":   map[string]any{"type": "INTEGER", "description": "Oxirgi necha soat, 1–72, standart 24"},
			"query":   str("Faqat shu so'z bor yangiliklar (ixtiyoriy)"),
			"limit":   map[string]any{"type": "INTEGER", "description": "Ko'pi bilan nechta, 1–40, standart 20"},
		})}}
}

type rssItem struct {
	Title string `xml:"title"`
	Link  string `xml:"link"`
	Desc  struct {
		Inner string `xml:",innerxml"` // ichida xom HTML ham bo'lishi mumkin
	} `xml:"description"`
	Description string `xml:"-"`
	PubDate     string `xml:"pubDate"`
	source      string
	at          time.Time
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func cleanHTML(s string) string {
	s = strings.NewReplacer("<![CDATA[", "", "]]>", "").Replace(s)
	s = tagRe.ReplaceAllString(html.UnescapeString(tagRe.ReplaceAllString(s, " ")), " ") // &lt;p&gt; ham teg
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, " "))), " ")
}

var rssDateLayouts = []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", time.RFC3339}

func parseRSSDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range rssDateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (n *News) Call(ctx context.Context, cfg Config, _ string, args map[string]any) (string, error) {
	ids := argString(args, "sources")
	if ids == "" {
		ids = cfg["sources"]
	}
	if ids == "" {
		ids = "kun,gazeta,daryo"
	}
	want := map[string]bool{}
	for _, id := range strings.Split(strings.ToLower(ids), ",") {
		want[strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(id), ".uz"))] = true
	}
	since := time.Now().Add(-time.Duration(argInt(args, "hours", 24, 1, 72)) * time.Hour)
	query := strings.ToLower(argString(args, "query"))
	var items []rssItem
	var failed []string
	for _, src := range newsSources {
		if !want[src.ID] {
			continue
		}
		u := src.URL
		if n.URLs != nil {
			u = n.URLs[src.ID]
		}
		data, err := getBody(ctx, n.HTTP, u, map[string]string{"User-Agent": "KotibBot/1.0 (+RSS)"})
		if err != nil {
			failed = append(failed, src.Name)
			continue
		}
		var feed struct {
			Items []rssItem `xml:"channel>item"`
		}
		if err := xml.Unmarshal(data, &feed); err != nil {
			failed = append(failed, src.Name)
			continue
		}
		for _, it := range feed.Items {
			it.source, it.at = src.Name, parseRSSDate(it.PubDate)
			it.Title, it.Description = cleanHTML(it.Title), cleanHTML(it.Desc.Inner)
			if !it.at.IsZero() && it.at.Before(since) {
				continue
			}
			if query != "" && !strings.Contains(strings.ToLower(it.Title+" "+it.Description), query) {
				continue
			}
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		msg := "Yangi maqola topilmadi."
		if len(failed) > 0 {
			msg += " Ochilmagan manbalar: " + strings.Join(failed, ", ") + " (web_search vositasidan foydalanish mumkin)."
		}
		return msg, nil
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.After(items[j].at) })
	if limit := argInt(args, "limit", 20, 1, 40); len(items) > limit {
		items = items[:limit]
	}
	var b strings.Builder
	for i, it := range items {
		desc := it.Description
		if r := []rune(desc); len(r) > 220 {
			desc = string(r[:220]) + "…"
		}
		when := ""
		if !it.at.IsZero() {
			when = it.at.In(uzLoc).Format("02.01 15:04") + ", "
		}
		fmt.Fprintf(&b, "%d. %s (%s%s)\n%s\n%s\n", i+1, it.Title, when, it.source, desc, strings.TrimSpace(it.Link))
	}
	if len(failed) > 0 {
		b.WriteString("\nOchilmagan manbalar: " + strings.Join(failed, ", "))
	}
	return b.String(), nil
}
