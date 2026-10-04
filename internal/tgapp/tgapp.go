// Package tgapp stores the Telegram application credentials (api_id / api_hash),
// set from the web UI and encrypted at rest.
package tgapp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
)

const settingKey = "telegram_app"

type Credentials struct {
	APIID   int    `json:"api_id"`
	APIHash string `json:"api_hash"`
}

func Validate(c Credentials) error {
	if c.APIID <= 0 {
		return errors.New("api_id 必须是正整数")
	}
	if b, err := hex.DecodeString(c.APIHash); err != nil || len(b) != 16 {
		return errors.New("api_hash 必须是 32 位十六进制字符串")
	}
	return nil
}

type Store struct {
	st  *store.Store
	box *seal.Box
}

func New(st *store.Store, box *seal.Box) *Store { return &Store{st: st, box: box} }

func (s *Store) Load(ctx context.Context) (*Credentials, error) {
	raw, err := s.st.GetSetting(ctx, settingKey)
	if err != nil {
		return nil, err
	}
	plain, err := s.box.Open(raw)
	if err != nil {
		return nil, errors.New("cannot decrypt telegram app credentials; TOKEN_ENC_KEY changed?")
	}
	var c Credentials
	if err := json.Unmarshal(plain, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) Save(ctx context.Context, c Credentials, now int64) error {
	if err := Validate(c); err != nil {
		return err
	}
	plain, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.st.PutSetting(ctx, settingKey, s.box.Seal(plain), now)
}
