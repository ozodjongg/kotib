//go:build gotd

package userbot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"golang.org/x/net/proxy"

	"github.com/ozodjongg/secretar_ai_agent/internal/config"
	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
	"github.com/ozodjongg/secretar_ai_agent/internal/miniapp"
)

// GotdConnector — gotd/td (MTProto) asosidagi Connector.
type GotdConnector struct{ cfg GotdConfig }

func NewGotdConnector(cfg GotdConfig) (Connector, error) {
	if cfg.AppID == 0 || cfg.AppHash == "" {
		return nil, errors.New("TG_APP_ID va TG_APP_HASH kerak (https://my.telegram.org)")
	}
	if cfg.Store == nil || cfg.Vault == nil {
		return nil, errors.New("store va vault kerak")
	}
	return &GotdConnector{cfg: cfg}, nil
}

// ---- Shifrlangan session storage ----

type sessionStorage struct {
	owner int64
	c     *GotdConnector
}

func (s sessionStorage) aad() []byte { return []byte("session:" + strconv.FormatInt(s.owner, 10)) }

func (s sessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	blob, err := s.c.cfg.Store.LoadSession(ctx, s.owner)
	if err != nil {
		return nil, session.ErrNotFound
	}
	return s.c.cfg.Vault.Open(blob, s.aad())
}

func (s sessionStorage) StoreSession(ctx context.Context, data []byte) error {
	blob, err := s.c.cfg.Vault.Seal(data, s.aad())
	if err != nil {
		return err
	}
	return s.c.cfg.Store.SaveSession(ctx, s.owner, blob)
}

// ---- Klient yaratish ----

func (c *GotdConnector) newClient(owner int64, h telegram.UpdateHandler) (*telegram.Client, error) {
	opts := telegram.Options{
		SessionStorage: sessionStorage{owner: owner, c: c},
		UpdateHandler:  h,
		// Har sessiya uchun BARQAROR qurilma ma'lumoti: doim bir xil (o'zgaruvchan barmoq izi shubhali).
		Device: telegram.DeviceConfig{
			DeviceModel:    "Desktop",
			SystemVersion:  "Linux",
			AppVersion:     "1.0",
			SystemLangCode: "en",
			LangCode:       "uz",
		},
	}
	if c.cfg.Proxy != "" {
		u, err := url.Parse(c.cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("proxy URL: %w", err)
		}
		d, err := proxy.FromURL(u, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("proxy: %w", err)
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("proxy dialer context'ni qo'llamaydi")
		}
		opts.Resolver = dcs.Plain(dcs.PlainOptions{
			Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return cd.DialContext(ctx, network, addr)
			},
		})
	}
	return telegram.NewClient(c.cfg.AppID, c.cfg.AppHash, opts), nil
}

// ---- Login ----

type loginAuth struct {
	phone string
	io    LoginIO
}

// Yangi akkaunt ro'yxatdan o'tkazilmaydi: faqat mavjud Telegram akkauntini ulash.
var errNoSignUp = errors.New("bu raqamda Telegram akkaunt yo'q (ro'yxatdan o'tish qo'llab-quvvatlanmaydi)")

func (loginAuth) SignUp(context.Context) (auth.UserInfo, error) { return auth.UserInfo{}, errNoSignUp }
func (loginAuth) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return errNoSignUp
}

func (a loginAuth) Phone(context.Context) (string, error) { return a.phone, nil }
func (a loginAuth) Password(ctx context.Context) (string, error) {
	return a.io.Password(ctx)
}
func (a loginAuth) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	return a.io.Code(ctx)
}

func (c *GotdConnector) Login(ctx context.Context, owner int64, phone string, io LoginIO) error {
	client, err := c.newClient(owner, nil)
	if err != nil {
		return err
	}
	return client.Run(ctx, func(ctx context.Context) error {
		flow := auth.NewFlow(loginAuth{phone: phone, io: io}, auth.SendCodeOptions{})
		return client.Auth().IfNecessary(ctx, flow)
		// 2FA paroli faqat Password() ichida ishlatiladi va hech qayerda saqlanmaydi.
	})
}

// ---- Run ----

func (c *GotdConnector) Run(ctx context.Context, owner int64, onReady func(Telegram), onMsg func(Incoming)) error {
	disp := tg.NewUpdateDispatcher()
	g := &gotdTG{users: map[int64]userInfo{}}

	disp.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		for _, usr := range e.Users {
			g.remember(usr)
		}
		m, ok := u.Message.(*tg.Message)
		if !ok || m.Out || m.Message == "" {
			return nil
		}
		pu, ok := m.PeerID.(*tg.PeerUser) // faqat shaxsiy chatlar (guruh/kanal — qo'llab-quvvatlanmaydi)
		if !ok {
			return nil
		}
		if config.IsTelegramService(pu.UserID) {
			return nil // login kodlari: pipeline'ga ham, AI'ga ham bormaydi
		}
		// Shaxsiy xabarlar ko'pincha UpdateShortMessage bo'lib keladi — unda foydalanuvchi
		// ma'lumoti (bot belgisi ham) yo'q. Avval kontaktlar keshi, keyin xabarning o'zi
		// (messages.getMessages foydalanuvchini to'liq qaytaradi).
		info, known := g.user(pu.UserID)
		if !known {
			g.refreshContacts(ctx)
			info, known = g.user(pu.UserID)
		}
		if !known {
			g.resolveSender(ctx, m.ID)
			info, known = g.user(pu.UserID)
		}
		contact := config.Contact{UserID: pu.UserID}
		if known {
			contact.IsContact = info.contact
			contact.IsBot = info.bot
			contact.Name, contact.Phone = info.name, info.phone
		} else {
			// Kimligi aniqlanmadi — xavfsiz tomon: bot deb hisoblaymiz (javob yozilmaydi).
			contact.IsBot = true
		}
		onMsg(Incoming{ChatID: pu.UserID, MsgID: m.ID, Text: m.Message, Contact: contact})
		return nil
	})

	// updates.Manager: qisqa (short) update'larni to'liq ko'rinishga o'tkazadi va
	// qisqa uzilishlardan keyin o'tkazib yuborilgan update'larni tiklaydi (gap recovery).
	gaps := updates.New(updates.Config{Handler: disp})

	client, err := c.newClient(owner, gaps)
	if err != nil {
		return err
	}
	g.api = client.API()
	g.sender = message.NewSender(g.api)

	err = client.Run(ctx, func(ctx context.Context) error {
		st, err := client.Auth().Status(ctx)
		if err != nil {
			return classify(err)
		}
		if !st.Authorized || st.User == nil {
			return &FatalError{Reason: "session avtorizatsiyadan chiqqan"}
		}
		g.refreshContacts(ctx)
		return gaps.Run(ctx, g.api, st.User.ID, updates.AuthOptions{
			OnStart: func(context.Context) { onReady(g) },
		})
	})
	return classify(err)
}

// ---- Telegram interfeysi implementatsiyasi ----

type userInfo struct {
	accessHash int64
	contact    bool
	bot        bool
	name       string
	phone      string
}

type gotdTG struct {
	api    *tg.Client
	sender *message.Sender

	mu           sync.Mutex
	users        map[int64]userInfo
	contactsAt   time.Time
	contactsList []miniapp.ContactInfo
}

const (
	maxCachedUsers  = 20000
	contactsRefresh = time.Minute // kontaktlar ro'yxatini tez-tez so'ramaslik (FLOOD_WAIT xavfi)
)

func (g *gotdTG) remember(u *tg.User) {
	if u == nil || u.AccessHash == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.users[u.ID]; !ok && len(g.users) >= maxCachedUsers {
		return
	}
	g.users[u.ID] = userInfo{
		accessHash: u.AccessHash, contact: u.Contact, bot: u.Bot,
		name: strings.TrimSpace(u.FirstName + " " + u.LastName), phone: u.Phone,
	}
}

func (g *gotdTG) user(id int64) (userInfo, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	u, ok := g.users[id]
	return u, ok
}

// refreshContacts kontaktlar keshini yangilaydi (daqiqasiga ko'pi bilan bir marta).
func (g *gotdTG) refreshContacts(ctx context.Context) {
	g.mu.Lock()
	if time.Since(g.contactsAt) < contactsRefresh {
		g.mu.Unlock()
		return
	}
	g.contactsAt = time.Now()
	g.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := g.api.ContactsGetContacts(cctx, 0)
	if err != nil {
		return
	}
	cc, ok := res.(*tg.ContactsContacts)
	if !ok {
		return
	}
	list := make([]miniapp.ContactInfo, 0, len(cc.Users))
	for _, uc := range cc.Users {
		u, ok := uc.(*tg.User)
		if !ok {
			continue
		}
		g.remember(u)
		if u.Bot || u.Deleted || u.Self || len(list) >= 1000 {
			continue
		}
		name := strings.TrimSpace(u.FirstName + " " + u.LastName)
		if name == "" {
			name = "(ismsiz)"
		}
		list = append(list, miniapp.ContactInfo{ID: u.ID, Name: name})
	}
	g.mu.Lock()
	g.contactsList = list
	g.mu.Unlock()
}

// resolveSender xabarni ID bo'yicha olib, jo'natuvchi ma'lumotini keshga yozadi.
func (g *gotdTG) resolveSender(ctx context.Context, msgID int) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := g.api.MessagesGetMessages(cctx, []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}})
	if err != nil {
		return
	}
	var users []tg.UserClass
	switch r := res.(type) {
	case *tg.MessagesMessages:
		users = r.Users
	case *tg.MessagesMessagesSlice:
		users = r.Users
	}
	for _, uc := range users {
		if u, ok := uc.(*tg.User); ok {
			g.remember(u)
		}
	}
}

func (g *gotdTG) peer(ctx context.Context, id int64) (*tg.InputPeerUser, error) {
	u, ok := g.user(id)
	if !ok {
		// Qayta ishga tushgandan keyin kesh bo'sh bo'lishi mumkin: kontaktlardan olamiz.
		g.refreshContacts(ctx)
		u, ok = g.user(id)
	}
	if !ok {
		return nil, errors.New("peer topilmadi")
	}
	return &tg.InputPeerUser{UserID: id, AccessHash: u.accessHash}, nil
}

func (g *gotdTG) SendReply(ctx context.Context, chatID int64, replyTo int, text string, typing time.Duration) error {
	p, err := g.peer(ctx, chatID)
	if err != nil {
		return err
	}
	b := g.sender.To(p)
	// "typing..." ~5 soniya ko'rinadi: yozish vaqtida har 4s da yangilab turamiz.
	deadline := time.Now().Add(typing)
	for time.Now().Before(deadline) {
		if err := b.TypingAction().Typing(ctx); err != nil {
			return classify(err)
		}
		wait := time.Until(deadline)
		if wait > 4*time.Second {
			wait = 4 * time.Second
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return err
		}
	}
	mb := &b.Builder
	if replyTo > 0 {
		mb = mb.Reply(replyTo)
	}
	if _, err := mb.Text(ctx, text); err != nil {
		return classify(err)
	}
	return nil
}

func (g *gotdTG) MarkRead(ctx context.Context, chatID int64, maxID int) error {
	p, err := g.peer(ctx, chatID)
	if err != nil {
		return err
	}
	_, err = g.api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: p, MaxID: maxID})
	return classify(err)
}

func (g *gotdTG) History(ctx context.Context, chatID int64, n int) ([]llm.Turn, error) {
	p, err := g.peer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	res, err := g.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: p, Limit: n})
	if err != nil {
		return nil, classify(err)
	}
	var msgs []tg.MessageClass
	switch r := res.(type) {
	case *tg.MessagesMessages:
		msgs = r.Messages
	case *tg.MessagesMessagesSlice:
		msgs = r.Messages
	default:
		return nil, nil
	}
	// Telegram yangidan eskiga beradi — teskari aylantiramiz.
	out := make([]llm.Turn, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		if m, ok := msgs[i].(*tg.Message); ok && m.Message != "" {
			out = append(out, llm.Turn{FromOwner: m.Out, Text: m.Message})
		}
	}
	return out, nil
}

func (g *gotdTG) Contacts(ctx context.Context) ([]miniapp.ContactInfo, error) {
	g.refreshContacts(ctx)
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]miniapp.ContactInfo(nil), g.contactsList...), nil
}

func (g *gotdTG) LogOut(ctx context.Context) error {
	_, err := g.api.AuthLogOut(ctx)
	return err
}

// ---- Xatolarni tasniflash ----

var fatalTypes = []string{
	"PEER_FLOOD", // akkaunt spam sifatida cheklangan — darhol to'xtash shart
	"AUTH_KEY_UNREGISTERED", "AUTH_KEY_DUPLICATED", "SESSION_REVOKED", "SESSION_EXPIRED",
	"USER_DEACTIVATED", "USER_DEACTIVATED_BAN", "PHONE_NUMBER_BANNED",
}

// classify gotd xatolarini FloodWaitError / FatalError ga aylantiradi.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return &FloodWaitError{Wait: d}
	}
	for _, t := range fatalTypes {
		if tgerr.Is(err, t) {
			return &FatalError{Reason: t}
		}
	}
	return err
}
