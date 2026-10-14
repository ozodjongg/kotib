-- Do'stni taklif qilish (referal). Akkauntga FK yo'q (subscriptions kabi): akkauntni
-- o'chirib qayta ochish taklifni yoki mukofotni qayta-qayta olishga imkon bermasin.
CREATE TABLE IF NOT EXISTS referral_codes (
    owner_id   BIGINT PRIMARY KEY,
    code       TEXT NOT NULL UNIQUE,         -- deep link: t.me/<bot>?start=ref_<code>
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Har odam faqat bir marta taklif qilingan bo'ladi (invitee_id PK).
CREATE TABLE IF NOT EXISTS referrals (
    invitee_id   BIGINT PRIMARY KEY,
    referrer_id  BIGINT NOT NULL CHECK (referrer_id <> invitee_id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    activated_at TIMESTAMPTZ,                -- do'st Telegram akkauntini uladi (mukofot bir marta)
    paid_at      TIMESTAMPTZ                 -- do'stning birinchi to'lovi (mukofot bir marta)
);
CREATE INDEX IF NOT EXISTS referrals_referrer ON referrals (referrer_id);

-- Bonus kunlar (referal mukofoti): to'langan obuna kabi to'liq xizmat, lekin Telegram pul
-- yechmaydi. Avtomatik yangilanadigan obunada bonus to'langan davrdan keyinga suriladi.
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS bonus_until TIMESTAMPTZ;
