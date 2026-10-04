package store

import (
	"errors"
	"testing"
)

func TestSettings(t *testing.T) {
	s := newStore(t)
	if _, err := s.GetSetting(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing setting err = %v", err)
	}
	if err := s.PutSetting(ctx, "k", []byte("v1"), 1); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSetting(ctx, "k", []byte("v2"), 2); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetSetting(ctx, "k")
	if err != nil || string(v) != "v2" {
		t.Fatalf("GetSetting = %q, %v", v, err)
	}
}
