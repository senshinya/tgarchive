//go:build linux

package botapiserver

import (
	"os/exec"
	"syscall"
)

// setPdeathsig makes the kernel send SIGTERM to telegram-bot-api when the OS thread that forked it
// dies — in practice when tgarchive is SIGKILLed, OOM-killed or crashes — so the child never outlives
// the app while holding port 8081 and its binlog. start keeps that thread alive for the child's lifetime.
func setPdeathsig(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
