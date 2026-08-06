//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func newUpdateCommand(scriptPath string) *exec.Cmd {
	quotedPath := strings.ReplaceAll(scriptPath, `"`, `""`)
	command := exec.Command("cmd.exe")
	command.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       fmt.Sprintf(`cmd.exe /D /S /C call "%s"`, quotedPath),
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return command
}

func startUpdateScript(scriptPath string) error {
	return newUpdateCommand(scriptPath).Start()
}
