-- Sinovdagi kunlik limit hisobi (billing.Service.UseQuota). Akkauntga FK yo'q:
-- o'chirib qayta ochish hisobni nolga tushirmasin. Faqat sinov kunlari yoziladi —
-- mijoz boshiga ko'pi bilan TRIAL_DAYS ta qator.
CREATE TABLE IF NOT EXISTS usage_daily (
    owner_id BIGINT NOT NULL,
    day      DATE NOT NULL,                  -- Toshkent kuni
    used     INT NOT NULL CHECK (used > 0),
    PRIMARY KEY (owner_id, day)
);
