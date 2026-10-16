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

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Akkaunt orqali ulangan plaginlar sozlamasidagi yashirin kalitlar (Mini App'ga berilmaydi).
const (
	KeyRefreshToken = "refresh_token"
	KeyAccount      = "account" // ulangan akkaunt (email) — ko'rsatish uchun
)

// ErrAccountRevoked — mijoz Google'da ruxsatni olib tashlagan (token bekor qilingan).
var ErrAccountRevoked = errors.New("akkaunt ulanishi bekor qilingan")

func youtubePlugin(o Options) Plugin {
	if o.YouTubeToken == nil {
		return soon("youtube", "YouTube", "▶️", "Video", "YouTube'da video qidirish va obunalaringizdagi yangi videolar.")
	}
	return &YouTube{Token: o.YouTubeToken, HTTP: o.HTTP, BaseURL: o.YouTubeURL}
}

// YouTube — mijozning YouTube (Google) akkaunti orqali: API kalit kerak emas, «Ulash» tugmasi
// Google rozilik sahifasini ochadi (faqat o'qish ruxsati: youtube.readonly).
type YouTube struct {
	Token   func(ctx context.Context, refreshToken string) (string, error)
	HTTP    *http.Client
	BaseURL string // default https://www.googleapis.com/youtube/v3
}

func (y *YouTube) Info() Info {
	return Info{ID: "youtube", Name: "YouTube", Icon: "▶️", Category: "Video", Connect: "google",
		Description: "YouTube akkauntingizni ulang: video qidirish va obuna bo'lgan kanallaringizdagi yangi videolar. Faqat o'qish — kotib hech narsa joylamaydi va o'zgartirmaydi.",
		Examples:    []string{"Har kuni 20:00 da obunalarimdagi bugungi yangi videolar", "Har dushanba — Go dasturlash bo'yicha yangi darslar"}}
}

func (y *YouTube) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{
		{Name: "youtube_search", Description: "YouTube'da video qidiradi: nom, kanal, sana va havola qaytaradi.",
			Params: obj(map[string]any{
				"query":          str("Qidiruv so'rovi"),
				"max_results":    map[string]any{"type": "INTEGER", "description": "1–10, standart 5"},
				"order":          map[string]any{"type": "STRING", "enum": []string{"relevance", "date", "viewCount"}, "description": "Saralash (ixtiyoriy)"},
				"published_days": map[string]any{"type": "INTEGER", "description": "Faqat oxirgi N kunda chiqqanlar (ixtiyoriy)"},
			}, "query")},
		{Name: "youtube_subscriptions", Description: "Egasi obuna bo'lgan kanallardagi oxirgi yangi videolar (eng yangisidan).",
			Params: obj(map[string]any{
				"days":        map[string]any{"type": "INTEGER", "description": "Oxirgi necha kun, 1–30, standart 1"},
				"max_results": map[string]any{"type": "INTEGER", "description": "1–20, standart 10"},
			})},
	}
}

const maxSubChannels = 25 // har chaqiruvda tekshiriladigan kanallar (kvota va tezlik)

func (y *YouTube) base() string {
	if y.BaseURL != "" {
		return strings.TrimRight(y.BaseURL, "/")
	}
	return "https://www.googleapis.com/youtube/v3"
}

// get — akkaunt tokeni bilan YouTube API so'rovi.
func (y *YouTube) get(ctx context.Context, cfg Config, path string, q url.Values, out any) error {
	return oauthGet(ctx, y.HTTP, y.Token, cfg, "YouTube", y.base()+path+"?"+q.Encode(), out)
}

// oauthGet — akkaunt (Google OAuth) orqali ulangan plagin uchun GET so'rov va JSON javob.
func oauthGet(ctx context.Context, c *http.Client, token func(context.Context, string) (string, error), cfg Config, name, u string, out any) error {
	rt := cfg[KeyRefreshToken]
	if rt == "" {
		return fmt.Errorf("%s akkaunti ulanmagan (Plaginlar → %s → Ulash)", name, name)
	}
	tok, err := token(ctx, rt)
	if err != nil {
		return ErrAccountRevoked
	}
	data, err := getBody(ctx, c, u, map[string]string{"Authorization": "Bearer " + tok})
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "401"):
			return ErrAccountRevoked
		case strings.Contains(err.Error(), "403"):
			return fmt.Errorf("%s so'rovni rad etdi (ruxsat yo'q yoki kunlik limit tugagan)", name)
		case strings.Contains(err.Error(), "404"):
			return fmt.Errorf("%s: topilmadi", name)
		}
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s javobini o'qib bo'lmadi", name)
	}
	return nil
}

func (y *YouTube) Call(ctx context.Context, cfg Config, tool string, args map[string]any) (string, error) {
	if tool == "youtube_subscriptions" {
		return y.subscriptions(ctx, cfg, args)
	}
	q := argString(args, "query")
	if q == "" {
		return "", errors.New("query kerak")
	}
	order := argString(args, "order")
	if order != "date" && order != "viewCount" {
		order = "relevance"
	}
	v := url.Values{"part": {"snippet"}, "type": {"video"}, "q": {q}, "order": {order},
		"maxResults": {fmt.Sprint(argInt(args, "max_results", 5, 1, 10))}}
	if d := argInt(args, "published_days", 0, 0, 365); d > 0 {
		v.Set("publishedAfter", time.Now().AddDate(0, 0, -d).UTC().Format(time.RFC3339))
	}
	var out struct {
		Items []struct {
			ID struct {
				VideoID string `json:"videoId"`
			} `json:"id"`
			Snippet snippet `json:"snippet"`
		} `json:"items"`
	}
	if err := y.get(ctx, cfg, "/search", v, &out); err != nil {
		return "", err
	}
	vids := make([]video, 0, len(out.Items))
	for _, it := range out.Items {
		vids = append(vids, video{ID: it.ID.VideoID, Snippet: it.Snippet})
	}
	return formatVideos(vids), nil
}

type snippet struct {
	Title        string `json:"title"`
	ChannelTitle string `json:"channelTitle"`
	PublishedAt  string `json:"publishedAt"`
}

type video struct {
	ID      string
	Snippet snippet
}

// subscriptions — obuna kanallarining "uploads" ro'yxatidan yangi videolar (har kanal 1 kvota birligi).
func (y *YouTube) subscriptions(ctx context.Context, cfg Config, args map[string]any) (string, error) {
	days := argInt(args, "days", 1, 1, 30)
	limit := argInt(args, "max_results", 10, 1, 20)
	since := time.Now().AddDate(0, 0, -days)
	var subs struct {
		Items []struct {
			Snippet struct {
				Title      string `json:"title"`
				ResourceID struct {
					ChannelID string `json:"channelId"`
				} `json:"resourceId"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := y.get(ctx, cfg, "/subscriptions", url.Values{"part": {"snippet"}, "mine": {"true"}, "maxResults": {"50"}, "order": {"relevance"}}, &subs); err != nil {
		return "", err
	}
	if len(subs.Items) == 0 {
		return "Akkauntda obunalar yo'q.", nil
	}
	var vids []video
	for i, s := range subs.Items {
		if i >= maxSubChannels || ctx.Err() != nil {
			break
		}
		ch := s.Snippet.ResourceID.ChannelID
		if !strings.HasPrefix(ch, "UC") {
			continue
		}
		var pl struct {
			Items []struct {
				Snippet        snippet `json:"snippet"`
				ContentDetails struct {
					VideoID string `json:"videoId"`
				} `json:"contentDetails"`
			} `json:"items"`
		}
		// Kanalning barcha yuklamalari ro'yxati: "UC…" → "UU…".
		if err := y.get(ctx, cfg, "/playlistItems", url.Values{"part": {"snippet,contentDetails"}, "playlistId": {"UU" + ch[2:]}, "maxResults": {"5"}}, &pl); err != nil {
			if errors.Is(err, ErrAccountRevoked) {
				return "", err
			}
			continue // bitta kanal xatosi butun natijani buzmasin
		}
		for _, it := range pl.Items {
			t, err := time.Parse(time.RFC3339, it.Snippet.PublishedAt)
			if err != nil || t.Before(since) {
				continue
			}
			sn := it.Snippet
			if sn.ChannelTitle == "" {
				sn.ChannelTitle = s.Snippet.Title
			}
			vids = append(vids, video{ID: it.ContentDetails.VideoID, Snippet: sn})
		}
	}
	if len(vids) == 0 {
		return fmt.Sprintf("Oxirgi %d kunda obunalaringizda yangi video yo'q.", days), nil
	}
	sort.SliceStable(vids, func(i, j int) bool { return vids[i].Snippet.PublishedAt > vids[j].Snippet.PublishedAt })
	if len(vids) > limit {
		vids = vids[:limit]
	}
	return formatVideos(vids), nil
}

func formatVideos(vids []video) string {
	if len(vids) == 0 {
		return "Hech narsa topilmadi."
	}
	var b strings.Builder
	for i, v := range vids {
		date := v.Snippet.PublishedAt
		if len(date) >= 10 {
			date = date[:10]
		}
		fmt.Fprintf(&b, "%d. %s — %s (%s)\nhttps://youtu.be/%s\n", i+1, unescape(v.Snippet.Title), unescape(v.Snippet.ChannelTitle), date, v.ID)
	}
	return b.String()
}

// unescape — YouTube nomlaridagi HTML belgilar (&#39; &amp; ...).
func unescape(s string) string {
	return strings.NewReplacer("&#39;", "'", "&quot;", `"`, "&amp;", "&", "&lt;", "<", "&gt;", ">").Replace(s)
}
