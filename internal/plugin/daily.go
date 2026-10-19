package plugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ozodjongg/secretar_ai_agent/internal/llm"
)

// Daily — kun so'zi (inglizcha so'z: talaffuz, tarjima, misol) va kun maqoli. Ro'yxat kod ichida:
// har kuni navbatdagisi, ro'yxat tugaguncha takrorlanmaydi; internet kerak emas.
type Daily struct {
	Now func() time.Time // test uchun; nil — time.Now
}

type word struct{ en, ipa, uz, example string }

var dailyWords = []word{
	{"achieve", "/əˈtʃiːv/", "erishmoq", "She worked hard to achieve her goals."},
	{"improve", "/ɪmˈpruːv/", "yaxshilamoq", "Reading every day will improve your English."},
	{"opportunity", "/ˌɒpəˈtjuːnəti/", "imkoniyat", "This job is a great opportunity for you."},
	{"decision", "/dɪˈsɪʒn/", "qaror", "It was a difficult decision."},
	{"experience", "/ɪkˈspɪəriəns/", "tajriba", "He has five years of experience in sales."},
	{"reliable", "/rɪˈlaɪəbl/", "ishonchli", "We need a reliable partner."},
	{"available", "/əˈveɪləbl/", "mavjud, bo'sh", "Are you available on Monday?"},
	{"schedule", "/ˈʃedjuːl/", "jadval, reja", "Let me check my schedule."},
	{"deadline", "/ˈdedlaɪn/", "oxirgi muddat", "The deadline is next Friday."},
	{"meeting", "/ˈmiːtɪŋ/", "uchrashuv, yig'ilish", "The meeting starts at ten."},
	{"suggest", "/səˈdʒest/", "taklif qilmoq", "I suggest we start early."},
	{"confirm", "/kənˈfɜːm/", "tasdiqlamoq", "Please confirm your order."},
	{"request", "/rɪˈkwest/", "so'rov; so'ramoq", "I sent a request yesterday."},
	{"customer", "/ˈkʌstəmə/", "mijoz", "The customer is always right."},
	{"purchase", "/ˈpɜːtʃəs/", "xarid; sotib olmoq", "Keep the receipt after your purchase."},
	{"discount", "/ˈdɪskaʊnt/", "chegirma", "They offer a ten percent discount."},
	{"invoice", "/ˈɪnvɔɪs/", "hisob-faktura", "We will send the invoice tomorrow."},
	{"budget", "/ˈbʌdʒɪt/", "byudjet", "We must stay within the budget."},
	{"income", "/ˈɪnkʌm/", "daromad", "Her monthly income has grown."},
	{"expense", "/ɪkˈspens/", "xarajat", "Rent is our biggest expense."},
	{"profit", "/ˈprɒfɪt/", "foyda", "The company made a profit this year."},
	{"growth", "/ɡrəʊθ/", "o'sish", "The city shows fast growth."},
	{"increase", "/ɪnˈkriːs/", "oshirmoq; o'sish", "Prices increase every year."},
	{"reduce", "/rɪˈdjuːs/", "kamaytirmoq", "Try to reduce sugar in your diet."},
	{"compare", "/kəmˈpeə/", "solishtirmoq", "Compare the prices before buying."},
	{"choose", "/tʃuːz/", "tanlamoq", "You can choose any color."},
	{"explain", "/ɪkˈspleɪn/", "tushuntirmoq", "Can you explain this rule?"},
	{"describe", "/dɪˈskraɪb/", "tasvirlamoq", "Describe your ideal day."},
	{"discuss", "/dɪˈskʌs/", "muhokama qilmoq", "We will discuss it later."},
	{"agree", "/əˈɡriː/", "rozi bo'lmoq", "I agree with you."},
	{"refuse", "/rɪˈfjuːz/", "rad etmoq", "He refused to answer."},
	{"allow", "/əˈlaʊ/", "ruxsat bermoq", "Smoking is not allowed here."},
	{"avoid", "/əˈvɔɪd/", "qochmoq, oldini olmoq", "Avoid driving in heavy traffic."},
	{"prepare", "/prɪˈpeə/", "tayyorlamoq", "I need to prepare for the exam."},
	{"remember", "/rɪˈmembə/", "eslamoq", "Remember to call your mother."},
	{"forget", "/fəˈɡet/", "unutmoq", "Don't forget your keys."},
	{"borrow", "/ˈbɒrəʊ/", "qarzga olmoq", "Can I borrow your pen?"},
	{"lend", "/lend/", "qarzga bermoq", "She lent me some money."},
	{"repair", "/rɪˈpeə/", "ta'mirlamoq", "They repaired the car quickly."},
	{"arrive", "/əˈraɪv/", "yetib kelmoq", "The train arrives at six."},
	{"delay", "/dɪˈleɪ/", "kechikish; kechiktirmoq", "Sorry for the delay."},
	{"cancel", "/ˈkænsl/", "bekor qilmoq", "The flight was cancelled."},
	{"journey", "/ˈdʒɜːni/", "sayohat, yo'l", "Have a safe journey!"},
	{"luggage", "/ˈlʌɡɪdʒ/", "yuk, bagaj", "My luggage is too heavy."},
	{"neighbour", "/ˈneɪbə/", "qo'shni", "Our neighbour is very kind."},
	{"relative", "/ˈrelətɪv/", "qarindosh", "We visited our relatives in Samarkand."},
	{"wedding", "/ˈwedɪŋ/", "to'y", "Their wedding is in June."},
	{"guest", "/ɡest/", "mehmon", "We have guests tonight."},
	{"invite", "/ɪnˈvaɪt/", "taklif qilmoq (mehmonga)", "They invited us to dinner."},
	{"grateful", "/ˈɡreɪtfl/", "minnatdor", "I am grateful for your help."},
	{"honest", "/ˈɒnɪst/", "halol, rostgo'y", "He is an honest man."},
	{"patient", "/ˈpeɪʃnt/", "sabrli; bemor", "Be patient, it takes time."},
	{"polite", "/pəˈlaɪt/", "xushmuomala", "Always be polite to customers."},
	{"brave", "/breɪv/", "jasur", "The brave boy saved the cat."},
	{"careful", "/ˈkeəfl/", "ehtiyotkor", "Be careful on the road."},
	{"curious", "/ˈkjʊəriəs/", "qiziquvchan", "Children are naturally curious."},
	{"confident", "/ˈkɒnfɪdənt/", "o'ziga ishongan", "She feels confident before the interview."},
	{"lazy", "/ˈleɪzi/", "dangasa", "Don't be lazy, get up!"},
	{"busy", "/ˈbɪzi/", "band", "I'm busy right now."},
	{"tired", "/ˈtaɪəd/", "charchagan", "I'm tired after work."},
	{"healthy", "/ˈhelθi/", "sog'lom", "Eat healthy food."},
	{"medicine", "/ˈmedsn/", "dori", "Take this medicine twice a day."},
	{"appointment", "/əˈpɔɪntmənt/", "uchrashuv (oldindan belgilangan)", "I have a doctor's appointment at three."},
	{"weather", "/ˈweðə/", "ob-havo", "The weather is nice today."},
	{"season", "/ˈsiːzn/", "fasl, mavsum", "Spring is my favourite season."},
	{"harvest", "/ˈhɑːvɪst/", "hosil", "The cotton harvest starts in September."},
	{"knowledge", "/ˈnɒlɪdʒ/", "bilim", "Knowledge is power."},
	{"skill", "/skɪl/", "ko'nikma, mahorat", "Communication is an important skill."},
	{"practice", "/ˈpræktɪs/", "mashq", "Practice makes perfect."},
	{"mistake", "/mɪˈsteɪk/", "xato", "Everyone makes mistakes."},
	{"success", "/səkˈses/", "muvaffaqiyat", "Hard work leads to success."},
	{"failure", "/ˈfeɪljə/", "muvaffaqiyatsizlik", "Failure is part of learning."},
	{"effort", "/ˈefət/", "harakat, kuch", "It takes a lot of effort."},
	{"goal", "/ɡəʊl/", "maqsad; gol", "What is your main goal this year?"},
	{"habit", "/ˈhæbɪt/", "odat", "Reading is a good habit."},
	{"advice", "/ədˈvaɪs/", "maslahat", "Can you give me some advice?"},
	{"answer", "/ˈɑːnsə/", "javob", "I know the answer."},
	{"question", "/ˈkwestʃən/", "savol", "May I ask a question?"},
	{"problem", "/ˈprɒbləm/", "muammo", "No problem at all."},
	{"solution", "/səˈluːʃn/", "yechim", "We found a solution."},
	{"reason", "/ˈriːzn/", "sabab", "What is the reason for the delay?"},
	{"result", "/rɪˈzʌlt/", "natija", "The results will be ready tomorrow."},
	{"probably", "/ˈprɒbəbli/", "ehtimol", "It will probably rain tonight."},
	{"usually", "/ˈjuːʒuəli/", "odatda", "I usually wake up at seven."},
	{"recently", "/ˈriːsntli/", "yaqinda", "I recently moved to Tashkent."},
	{"immediately", "/ɪˈmiːdiətli/", "darhol", "Please call me immediately."},
	{"although", "/ɔːlˈðəʊ/", "garchi", "Although it was late, we kept working."},
	{"however", "/haʊˈevə/", "biroq", "It's expensive. However, it's worth it."},
	{"enough", "/ɪˈnʌf/", "yetarli", "Do we have enough time?"},
	{"especially", "/ɪˈspeʃəli/", "ayniqsa", "I love fruit, especially apples."},
	{"environment", "/ɪnˈvaɪrənmənt/", "atrof-muhit", "We must protect the environment."},
	{"pollution", "/pəˈluːʃn/", "ifloslanish", "Air pollution is high in winter."},
	{"population", "/ˌpɒpjuˈleɪʃn/", "aholi", "The population of the city is growing."},
	{"government", "/ˈɡʌvənmənt/", "hukumat", "The government announced new rules."},
	{"law", "/lɔː/", "qonun", "Everyone must follow the law."},
	{"rent", "/rent/", "ijara; ijaraga olmoq", "We rent a flat near the metro."},
	{"salary", "/ˈsæləri/", "maosh", "She got a higher salary."},
	{"employee", "/ɪmˈplɔɪiː/", "xodim", "The company has fifty employees."},
	{"interview", "/ˈɪntəvjuː/", "suhbat (ishga)", "I have a job interview tomorrow."},
	{"responsible", "/rɪˈspɒnsəbl/", "mas'ul", "Who is responsible for this project?"},
}

var dailyProverbs = []string{
	"Bilim — baxt kaliti.",
	"Ilm olish — igna bilan quduq qazish.",
	"Bilmaganni so'rab o'rgangan olim, orlanib so'ramagan o'ziga zolim.",
	"Mehnatning tagi — rohat.",
	"Mehnat qilsang, rohat ko'rasan.",
	"Ishlagan tishlaydi.",
	"Bugungi ishni ertaga qo'yma.",
	"Sabr tagi — sariq oltin.",
	"Shoshilgan qiz erga yolchimas.",
	"Yetti o'lchab, bir kes.",
	"Tomchi-tomchi ko'l bo'lur.",
	"Do'st boshga, dushman oyoqqa qaraydi.",
	"Do'stsiz boshim — tuzsiz oshim.",
	"Yaxshi do'st — qora kunda bilinar.",
	"Ming do'st ham oz, bir dushman ham ko'p.",
	"Eski do'st dushman bo'lmas, eski dushman do'st bo'lmas.",
	"Qo'shni haqqi — Tangri haqqi.",
	"Uying tor bo'lsa ham, ko'ngling keng bo'lsin.",
	"Mehmon otangdan ulug'.",
	"Til — dil kaliti.",
	"Yaxshi so'z — jon ozig'i, yomon so'z — bosh qozig'i.",
	"Avval o'yla, keyin so'yla.",
	"Ko'p gapirgan ko'p yanglishar.",
	"Aytilgan so'z — otilgan o'q.",
	"Ona yurting — oltin beshiging.",
	"O'z uyim — o'lan to'shagim.",
	"Vatanni sevmoq — iymondandir.",
	"Bulbul chamanni sevar, odam — Vatanni.",
	"Otang bolasi bo'lma, odam bolasi bo'l.",
	"Ota-onaning qadrini bola bo'lganda bilasan.",
	"Onasini ko'rib qizini ol, chetini ko'rib bo'zini ol.",
	"Qush uyasida ko'rganini qiladi.",
	"Kattaga hurmat, kichikka izzat.",
	"Birlashgan o'zar, birlashmagan to'zar.",
	"Arzon go'shtning sho'rvasi tatimas.",
	"Ko'z — qo'rqoq, qo'l — botir.",
	"Harakatda — barakat.",
	"Ishning boshi qiyin.",
	"Yaxshilik qil, suvga tashla — baliq bilar, baliq bilmasa Xoliq bilar.",
	"Yaxshidan ot qoladi, yomondan dog' qoladi.",
	"Sog'lik — tuman boylik.",
	"Kasalni yashirsang, isitmasi oshkor qilar.",
	"Qanoat — tuganmas boylik.",
	"Yolg'onchining uyi yonibdi — hech kim ishonmabdi.",
	"To'g'ri so'z — tuqqaningga yoqmas.",
	"Vaqt — oltindan qimmat.",
	"Oz-oz o'rganib dono bo'lur, qatra-qatra yig'ilib daryo bo'lur.",
	"O'qigan o'zar, o'qimagan to'zar.",
	"Chumchuqdan qo'rqqan tariq ekmas.",
}

func (d *Daily) Info() Info {
	return Info{ID: "daily", Name: "Kun so'zi va maqoli", Icon: "💡", Category: "Kundalik",
		Description: "Har kuni bitta inglizcha so'z (talaffuzi, tarjimasi, misol gap) va bitta o'zbek xalq maqoli. Ro'yxat tugaguncha takrorlanmaydi; internet kerak emas.",
		Fields: []Field{{Key: "kind", Label: "Nima yuborilsin", Type: FieldSelect, Default: "both",
			Options: []Option{{"both", "So'z va maqol"}, {"word", "Faqat inglizcha so'z"}, {"proverb", "Faqat maqol"}}}},
		Examples: []string{"Har kuni 08:00 da kun so'zi va maqoli — so'zni qanday ishlatish haqida yana bitta misol qo'sh"}}
}

func (d *Daily) Tools() []llm.ToolDecl {
	return []llm.ToolDecl{{Name: "daily_word", Description: "Bugungi inglizcha so'z (talaffuz, o'zbekcha tarjima, misol) va/yoki bugungi o'zbek maqoli.",
		Params: obj(map[string]any{
			"kind":  map[string]any{"type": "STRING", "enum": []string{"both", "word", "proverb"}, "description": "Ixtiyoriy, standart — egasi tanlagani"},
			"count": map[string]any{"type": "INTEGER", "description": "Nechta so'z, 1–5, standart 1"},
		})}}
}

// dayIndex — kun raqami (Toshkent vaqti bo'yicha): har kuni bittaga oshadi.
func dayIndex(t time.Time) int {
	t = t.In(uzLoc)
	return int(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func (d *Daily) Call(_ context.Context, cfg Config, _ string, args map[string]any) (string, error) {
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	n := dayIndex(now())
	kind := argString(args, "kind")
	if kind != "word" && kind != "proverb" && kind != "both" {
		kind = cfg["kind"]
	}
	var b strings.Builder
	if kind != "proverb" {
		count := argInt(args, "count", 1, 1, 5)
		for i := 0; i < count; i++ {
			w := dailyWords[(n*count+i)%len(dailyWords)]
			if i == 0 {
				b.WriteString("📘 Kun so'zi:\n")
			}
			fmt.Fprintf(&b, "%s %s — %s\nMisol: %s\n", w.en, w.ipa, w.uz, w.example)
		}
	}
	if kind != "word" {
		fmt.Fprintf(&b, "🪶 Kun maqoli: %s\n", dailyProverbs[n%len(dailyProverbs)])
	}
	return b.String(), nil
}
