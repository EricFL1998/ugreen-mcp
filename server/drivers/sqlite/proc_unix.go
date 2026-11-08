//go:build linux || darwin

package sqlite

import (
	"os/exec"
	"syscall"
)

// 平台相关：进程组与信号。Linux（NAS 目标平台）与 macOS（开发机）语义一致。
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func syscallTerm() syscall.Signal { return syscall.SIGTERM }

func syscallKill() syscall.Signal { return syscall.SIGKILL }

// signalGroup 先杀整个进程组（python 若有子进程一并收掉），失败再退化为杀进程本身。
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		_ = cmd.Process.Signal(sig)
	}
}
