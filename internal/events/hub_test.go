package events

import (
	"testing"
	"time"
)

func TestPublishSubscribe(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	h.Publish(Event{Type: "message.created", Data: 1})
	select {
	case e := <-ch:
		if e.Type != "message.created" {
			t.Fatalf("event = %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	cancel()
	h.Publish(Event{Type: "after"}) // must not panic or block after unsubscribe
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe()
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			h.Publish(Event{Type: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}
