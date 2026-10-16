package plugin

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/ozodjongg/secretar_ai_agent/internal/store"
)

// Vault — sozlamalarni shifrlash (crypto.Vault).
type Vault interface {
	Seal(plaintext, aad []byte) ([]byte, error)
	Open(blob, aad []byte) ([]byte, error)
}

// aad shifrlangan sozlamani egasi va plaginga bog'laydi: bir egadagi blobni boshqasiga
// (yoki boshqa plaginga) ko'chirib bo'lmaydi.
func aad(owner int64, id string) []byte {
	return []byte("plugin:" + strconv.FormatInt(owner, 10) + ":" + id)
}

// Seal sozlamani shifrlaydi (bo'sh sozlama — nil).
func Seal(v Vault, owner int64, id string, cfg Config) ([]byte, error) {
	if len(cfg) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return v.Seal(raw, aad(owner, id))
}

// Open shifrlangan sozlamani ochadi.
func Open(v Vault, owner int64, id string, blob []byte) (Config, error) {
	if len(blob) == 0 {
		return Config{}, nil
	}
	raw, err := v.Open(blob, aad(owner, id))
	if err != nil {
		return nil, err
	}
	cfg := Config{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Load — egasining barcha plagin yozuvlari sozlamasi ochilgan holda (ochib bo'lmaganlari tashlab ketiladi).
func Load(ctx context.Context, st store.Store, v Vault, owner int64) (map[string]store.PluginConfig, map[string]Config, error) {
	rows, err := st.ListPluginConfigs(ctx, owner)
	if err != nil {
		return nil, nil, err
	}
	recs, cfgs := map[string]store.PluginConfig{}, map[string]Config{}
	for _, r := range rows {
		cfg, err := Open(v, owner, r.PluginID, r.Config)
		if err != nil {
			continue
		}
		recs[r.PluginID], cfgs[r.PluginID] = r, cfg
	}
	return recs, cfgs, nil
}

// EnabledFor — egasida yoqilgan (va reestrda mavjud) plaginlar.
func (r *Registry) EnabledFor(ctx context.Context, st store.Store, v Vault, owner int64) []Enabled {
	recs, cfgs, err := Load(ctx, st, v, owner)
	if err != nil {
		return nil
	}
	var out []Enabled
	for _, p := range r.list {
		id := p.Info().ID
		if rec, ok := recs[id]; ok && rec.Enabled && !p.Info().Soon {
			out = append(out, Enabled{ID: id, Config: cfgs[id]})
		}
	}
	return out
}

// SaveAccount — akkaunt orqali ulangan plagin (OAuth) tokenini shifrlab saqlaydi va plaginni yoqadi.
func SaveAccount(ctx context.Context, st store.Store, v Vault, owner int64, id, refreshToken, account string) error {
	sealed, err := Seal(v, owner, id, Config{KeyRefreshToken: refreshToken, KeyAccount: account})
	if err != nil {
		return err
	}
	return st.SavePluginConfig(ctx, store.PluginConfig{OwnerID: owner, PluginID: id, Enabled: true, Config: sealed})
}
