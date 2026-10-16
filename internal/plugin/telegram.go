package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// FeedPost — kanal yoki guruhdagi bitta xabar.
type FeedPost struct {
	At   time.Time
	From string // guruhda — yozgan odam (kanalda bo'sh)
	Text string
	Link string // https://t.me/<username>/<id>; ochiq bo'lmasa bo'sh
}

// FeedChat — bitta kanal yoki guruh va undagi yangi xabarlar (eskidan yangiga).
type FeedChat struct {
	Title string
	Kind  string // channel | group
	Posts []FeedPost
}

// TelegramFeed — egasining Telegram akkauntidan kanal/guruh xabarlarini o'qiydi
// (userbot.Manager.Feed). kind: "channels" | "groups".
type TelegramFeed func(ctx context.Context, owner int64, kind string, since time.Time, maxChats int) ([]FeedChat, error)

// Telegram — obuna bo'lgan kanallar va guruhlardagi yangi xabarlar xulosasi: kotib akkauntga
// allaqachon ulangan, tashqi kalit kerak emas.
type Telegram struct{ Feed TelegramFeed }

func (t *Telegram) Info() Info {
	return Info{ID: "telegram", Name: "Telegram kanallar va guruhlar", Icon: "📢", Category: "Telegram",
		Description: "Obuna bo'lgan kanallaringiz va guruhlaringizdagi yangi xabarlarni o'qib, qisqa xulosa qiladi — yuzlab postni o'qishga vaqt ketmaydi. Arxivlangan chatlar o'qilmaydi; xabarlar xulosa uchun AI'ga yuboriladi.",
		Examples:    []string{"Har kuni 21:00 da kanallarimdagi bugungi eng muhim 10 ta yangilik", "Har kuni 18:00 da ish guruhlarida meni nima kutyapti"}}
}

func (t *Telegram) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "telegram_feed",
		Description: "Egasining Telegram kanallari yoki guruhlaridagi oxirgi xabarlar (xulosa qilish uchun). Arxivdagi chatlar kirmaydi.",
		Params: obj(map[string]any{
			"kind":      map[string]any{"type": "STRING", "enum": []string{"channels", "groups"}, "description": "channels — kanallar (standart), groups — guruhlar"},
			"hours":     map[string]any{"type": "INTEGER", "description": "Oxirgi necha soat, 1–72, standart 24"},
			"max_chats": map[string]any{"type": "INTEGER", "description": "Ko'pi bilan nechta chat, 1–30, standart 15"},
		})}}
}

const (
	maxPostRunes  = 600
	maxFeedOutput = 7500
)

func (t *Telegram) Call(ctx context.Context, _ Config, _ string, args map[string]any) (string, error) {
	owner := OwnerFrom(ctx)
	if owner == 0 || t.Feed == nil {
		return "", errors.New("Telegram akkaunt topilmadi")
	}
	kind := argString(args, "kind")
	if kind != "groups" {
		kind = "channels"
	}
	hours := argInt(args, "hours", 24, 1, 72)
	chats, err := t.Feed(ctx, owner, kind, time.Now().Add(-time.Duration(hours)*time.Hour), argInt(args, "max_chats", 15, 1, 30))
	if err != nil {
		return "", err
	}
	what := map[string]string{"channels": "kanallaringizda", "groups": "guruhlaringizda"}[kind]
	if len(chats) == 0 {
		return fmt.Sprintf("Oxirgi %d soatda %s yangi xabar yo'q.", hours, what), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Oxirgi %d soatda %s (%d ta chat):\n", hours, what, len(chats))
	for _, c := range chats {
		if b.Len() > maxFeedOutput {
			b.WriteString("\n…(qolganlari qisqartirildi)")
			break
		}
		fmt.Fprintf(&b, "\n### %s (%d ta xabar)\n", c.Title, len(c.Posts))
		for _, p := range c.Posts {
			text := strings.Join(strings.Fields(p.Text), " ")
			if r := []rune(text); len(r) > maxPostRunes {
				text = string(r[:maxPostRunes]) + "…"
			}
			line := "- " + p.At.Format("15:04") + " "
			if p.From != "" {
				line += p.From + ": "
			}
			line += text
			if p.Link != "" {
				line += " (" + p.Link + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String(), nil
}
