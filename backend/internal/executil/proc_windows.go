//go:build windows

package executil

import (
	"os/exec"
	"syscall"
)

// createNoWindow 对应 Win32 API 的 CREATE_NO_WINDOW。
const createNoWindow = 0x08000000

func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

func kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
