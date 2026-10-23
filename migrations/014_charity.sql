-- Hayriya: obuna to'lovlaridan ulush (CHARITY_PERCENT) va Stars xayriyalari oyma-oy hisoblanadi.
-- To'lovlar payments jadvalida qoladi (xayriya payload'i "don:" bilan boshlanadi); bu yerda —
-- hisob boshlangan oy va o'tkazmalar (oylik hisob yoki adminning qo'shimcha xayriyasi) isboti bilan.
-- Akkauntga FK yo'q: moliyaviy yozuvlar akkaunt o'chirilganda ham saqlanadi.
CREATE TABLE IF NOT EXISTS charity_settings (
    id       INT PRIMARY KEY CHECK (id = 1),
    start_at TIMESTAMPTZ NOT NULL                -- hisob shu oydan boshlanadi (avvalgi to'lovlar kirmaydi)
);

CREATE TABLE IF NOT EXISTS charity_payouts (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('monthly', 'manual')),
    period     TEXT NOT NULL DEFAULT '',         -- "2026-10" (monthly); manual — bo'sh
    share      INT NOT NULL DEFAULT 0,           -- shu oy obunalaridan ulush
    donated    INT NOT NULL DEFAULT 0,           -- shu oy Stars xayriyalari
    adjust     INT NOT NULL DEFAULT 0,           -- oldingi oylar tuzatishi (refund, yaxlitlash)
    stars      INT NOT NULL CHECK (stars >= 0),  -- o'tkaziladigan jami
    note       TEXT NOT NULL DEFAULT '',
    proof      TEXT NOT NULL DEFAULT '',         -- TON tx hash / kvitansiya havolasi
    paid       BOOLEAN NOT NULL DEFAULT false,
    by_admin   BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at    TIMESTAMPTZ
);
-- Har oy uchun bitta hisob: ikki nusxa/qayta ishga tushish ikki marta yozmasin.
CREATE UNIQUE INDEX IF NOT EXISTS charity_payouts_period ON charity_payouts (period) WHERE kind = 'monthly';
