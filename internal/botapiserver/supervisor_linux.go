//go:build linux

package botapiserver

import "os/exec"

func setPdeathsig(cmd *exec.Cmd) {}
