package seal

import (
	"bytes"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestRoundTrip(t *testing.T) {
	b, err := New(key(1))
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := b.Seal([]byte("123:secret")), b.Seal([]byte("123:secret"))
	if bytes.Equal(c1, c2) {
		t.Fatal("nonce must differ between seals")
	}
	got, err := b.Open(c1)
	if err != nil || string(got) != "123:secret" {
		t.Fatalf("open = %q, %v", got, err)
	}
}

func TestOpenRejectsTamperAndWrongKey(t *testing.T) {
	b, _ := New(key(1))
	other, _ := New(key(2))
	c := b.Seal([]byte("x"))
	c[len(c)-1] ^= 1
	if _, err := b.Open(c); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
	if _, err := other.Open(b.Seal([]byte("x"))); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := b.Open([]byte{1, 2}); err == nil {
		t.Fatal("short input must fail")
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("expected error")
	}
}
