// Package executil 封装跨平台子进程细节（隐藏 Windows 黑窗、独立进程组）。
package executil

import "os/exec"

// Prepare 为命令注入平台相关的系统属性。
func Prepare(cmd *exec.Cmd) {
	prepare(cmd)
}

// Kill 强制结束命令，Unix 下会结束整个进程组，避免 ffmpeg 僵尸进程。
func Kill(cmd *exec.Cmd) {
	kill(cmd)
}
