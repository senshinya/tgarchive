package tgapp

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"tgarchive/internal/seal"
	"tgarchive/internal/store"
)

const hash = "0123456789abcdef0123456789abcdef"

func TestValidate(t *testing.T) {
	if err := Validate(Credentials{APIID: 123, APIHash: hash}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Credentials{{0, hash}, {-1, hash}, {1, "short"}, {1, strings.Repeat("z", 32)}, {1, ""}} {
		if Validate(c) == nil {
			t.Fatalf("%+v must be invalid", c)
		}
	}
}

func TestSaveLoad(t *testing.T) {
	ctx := context.Background()
	st, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	s := New(st, box)
	if _, err := s.Load(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unset = %v", err)
	}
	if err := s.Save(ctx, Credentials{APIID: 0, APIHash: hash}, 1); err == nil {
		t.Fatal("invalid credentials must not be saved")
	}
	if err := s.Save(ctx, Credentials{APIID: 123, APIHash: hash}, 1); err != nil {
		t.Fatal(err)
	}
	raw, _ := st.GetSetting(ctx, settingKey)
	if bytes.Contains(raw, []byte(hash)) {
		t.Fatal("api_hash stored in plaintext")
	}
	c, err := s.Load(ctx)
	if err != nil || c.APIID != 123 || c.APIHash != hash {
		t.Fatalf("Load = %+v, %v", c, err)
	}
}
