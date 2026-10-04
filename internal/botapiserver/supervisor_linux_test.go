//go:build linux

package botapiserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
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
