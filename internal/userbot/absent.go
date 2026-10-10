package userbot

import (
	"sync"
	"time"
)

const (
	absentTTL = 30 * time.Minute
	absentMax = 1000 // entries kept at most; the oldest go first
)

// AbsentChannels remembers the channels a complete scan of the account's dialogs did not find:
// a private channel without a username can only be found that way, and once the account has left
// it (or was removed, or the channel deleted) every lookup would page through all dialogs again,
// a call Telegram throttles hard. Background work takes a recorded channel as not joined until the
// entry expires (TTL); a scan that does find a channel clears its entry. Kept in memory only.
type AbsentChannels struct {
	TTL time.Duration

	mu sync.Mutex
	at map[int64]time.Time // when a scan last missed each channel
}

func NewAbsentChannels() *AbsentChannels {
	return &AbsentChannels{TTL: absentTTL, at: map[int64]time.Time{}}
}

// has reports whether a scan missed channel id less than TTL before now. A nil set has nothing.
func (a *AbsentChannels) has(id int64, now time.Time) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.at[id]
	if ok && now.Sub(t) >= a.TTL {
		delete(a.at, id)
		return false
	}
	return ok
}

// add records that a complete scan at now did not find channel id, sweeping expired entries and,
// past absentMax, the oldest.
func (a *AbsentChannels) add(id int64, now time.Time) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, t := range a.at {
		if now.Sub(t) >= a.TTL {
			delete(a.at, k)
		}
	}
	delete(a.at, id)
	for len(a.at) >= absentMax {
		var oldest int64
		var first time.Time
		for k, t := range a.at {
			if first.IsZero() || t.Before(first) {
				oldest, first = k, t
			}
		}
		delete(a.at, oldest)
	}
	a.at[id] = now
}

// remove clears the entries of channels a scan found.
func (a *AbsentChannels) remove(ids ...int64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range ids {
		delete(a.at, id)
	}
}

func (a *AbsentChannels) size() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.at)
}
