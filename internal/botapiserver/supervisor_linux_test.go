//go:build linux

package botapiserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"tgarchive/internal/tgapp"
)

func TestSetPdeathsig(t *testing.T) {
	cmd := exec.Command("true")
	setPdeathsig(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Pdeathsig != syscall.SIGTERM {
		t.Fatalf("SysProcAttr = %+v, want Pdeathsig SIGTERM", cmd.SysProcAttr)
	}
}

// A SIGKILLed or OOM-killed tgarchive must not leave telegram-bot-api holding port 8081.
func TestChildTerminatedWhenParentDies(t *testing.T) {
	dir := t.TempDir()
	term := filepath.Join(dir, "term.txt")
	parent := exec.Command(os.Args[0])
	parent.Env = append(os.Environ(),
		"FAKE_PARENT=1",
		"FAKE_DIR="+dir,
		"FAKE_RECORD="+filepath.Join(dir, "record.txt"),
		"FAKE_TERM_RECORD="+term,
	)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { parent.Process.Kill() }) // no-op once the test has killed it
	childPID := 0
	eventually(t, "child started", func() bool {
		for _, l := range lines(term) {
			if n, ok := strings.CutPrefix(l, "start "); ok {
				childPID, _ = strconv.Atoi(n)
				return true
			}
		}
		return false
	})
	t.Cleanup(func() { syscall.Kill(childPID, syscall.SIGKILL) })

	parent.Process.Kill()
	parent.Wait()
	eventually(t, "child got SIGTERM after its parent died", func() bool {
		return slices.Contains(lines(term), "term")
	})
}

// The runtime terminates the OS thread of a goroutine that exits while locked to it. If that is the
// thread which forked the child, Pdeathsig fires and the healthy child is killed.
func TestChildSurvivesOSThreadChurn(t *testing.T) {
	s, record := newSup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, &tgapp.Credentials{APIID: 1, APIHash: hash1})
	eventually(t, "running", func() bool {
		st, _ := s.Status()
		return st == StateRunning && len(lines(record)) == 1
	})
	for i := 0; i < 200; i++ {
		var wg sync.WaitGroup
		for j := 0; j < 8; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runtime.LockOSThread() // exits still locked: the runtime retires this OS thread
			}()
		}
		wg.Wait()
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(lines(record)); n != 1 {
		t.Fatalf("child was restarted %d time(s) during OS thread churn", n-1)
	}
}
