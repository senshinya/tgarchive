// Package botapiserver runs the official telegram-bot-api server as a child process,
// using api_id / api_hash configured from the web UI.
package botapiserver

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"

	"tgarchive/internal/tgapp"
)

const (
	StateUnconfigured = "unconfigured"
	StateRunning      = "running"
	StateRestarting   = "restarting"
)

type Supervisor struct {
	Binary     string
	Dir        string
	TempDir    string
	Port       int
	Env        []string // extra child environment (tests)
	MinBackoff time.Duration
	MaxBackoff time.Duration

	applyMu sync.Mutex
	apply   chan tgapp.Credentials

	mu      sync.Mutex
	state   string
	lastErr string
}

func New(binary, dir, tempDir string, port int) *Supervisor {
	return &Supervisor{
		Binary: binary, Dir: dir, TempDir: tempDir, Port: port,
		MinBackoff: time.Second, MaxBackoff: 30 * time.Second,
		apply: make(chan tgapp.Credentials, 1),
		state: StateUnconfigured,
	}
}

// Apply hands new credentials to Run, replacing any not yet picked up.
func (s *Supervisor) Apply(c tgapp.Credentials) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	select {
	case <-s.apply:
	default:
	}
	s.apply <- c
}

func (s *Supervisor) Status() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.lastErr
}

func (s *Supervisor) set(state, lastErr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state, s.lastErr = state, lastErr
}

func (s *Supervisor) Run(ctx context.Context, initial *tgapp.Credentials) {
	cur := initial
	backoff := s.MinBackoff
	for {
		if cur == nil {
			s.set(StateUnconfigured, "")
			select {
			case <-ctx.Done():
				return
			case c := <-s.apply:
				cur = &c
			}
		}
		cmd, done, err := s.start(*cur)
		if err != nil {
			log.Printf("botapi: start telegram-bot-api: %v", err)
			s.set(StateRestarting, err.Error())
		} else {
			s.set(StateRunning, "")
			started := time.Now()
			select {
			case <-ctx.Done():
				s.stop(cmd, done)
				return
			case c := <-s.apply:
				s.stop(cmd, done)
				cur, backoff = &c, s.MinBackoff
				continue
			case err := <-done:
				msg := "exited"
				if err != nil {
					msg = err.Error()
				}
				log.Printf("botapi: telegram-bot-api %s", msg)
				s.set(StateRestarting, msg)
				if time.Since(started) > time.Minute {
					backoff = s.MinBackoff
				}
			}
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case c := <-s.apply:
			t.Stop()
			cur, backoff = &c, s.MinBackoff
			continue
		case <-t.C:
		}
		backoff = min(backoff*2, s.MaxBackoff)
	}
}

func (s *Supervisor) start(c tgapp.Credentials) (*exec.Cmd, <-chan error, error) {
	for _, d := range []string{s.Dir, s.TempDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, nil, err
		}
	}
	cmd := exec.Command(s.Binary,
		"--local",
		"--dir="+s.Dir,
		"--temp-dir="+s.TempDir,
		fmt.Sprintf("--http-port=%d", s.Port),
		"--http-ip-address=127.0.0.1",
	)
	// Deliberately not inheriting os.Environ(): the child must not see TOKEN_ENC_KEY.
	cmd.Env = append([]string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		fmt.Sprintf("TELEGRAM_API_ID=%d", c.APIID),
		"TELEGRAM_API_HASH=" + c.APIHash,
	}, s.Env...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	setPdeathsig(cmd)
	// Pdeathsig follows the forking OS thread, not the process. Start and reap the child on one locked
	// thread that does nothing else, so the runtime can never retire that thread under a live child.
	started := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		done <- cmd.Wait()
	}()
	if err := <-started; err != nil {
		return nil, nil, err
	}
	return cmd, done, nil
}

func (s *Supervisor) stop(cmd *exec.Cmd, done <-chan error) {
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}
