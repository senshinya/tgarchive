//go:build !linux

package botapiserver

import "os/exec"

// setPdeathsig is Linux-only; elsewhere the child is stopped only by Supervisor.stop.
func setPdeathsig(cmd *exec.Cmd) {}
