-- Telegram Stars obunasi.
-- subscriptions/payments akkauntga FK bilan bog'lanmagan: akkaunt o'chirilganda ham
-- moliyaviy yozuvlar (faqat ID, sana, summa — xabar matni yo'q) saqlanib qoladi va
-- bepul sinov muddatini qayta-qayta olib bo'lmaydi.
CREATE TABLE IF NOT EXISTS subscriptions (
    owner_id     BIGINT PRIMARY KEY,
    trial_until  TIMESTAMPTZ,
    paid_until   TIMESTAMPTZ,
    charge_id    TEXT NOT NULL DEFAULT '',   -- joriy obunaning oxirgi to'lovi (bekor qilish uchun)
    canceled     BOOLEAN NOT NULL DEFAULT false,
    reminded_for TIMESTAMPTZ,                -- qaysi tugash sanasi uchun eslatma yuborilgan
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS payments (
    charge_id  TEXT PRIMARY KEY,             -- telegram_payment_charge_id: takroriy yozuvdan himoya
    owner_id   BIGINT NOT NULL,
    amount     INT NOT NULL CHECK (amount > 0),
    currency   TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '',
    recurring  BOOLEAN NOT NULL DEFAULT false,
    refunded   BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS payments_owner ON payments (owner_id, created_at DESC);
