//go:build windows

package main

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestUpdateCommandRunsWithoutVisibleConsole(t *testing.T) {
	command := newUpdateCommand(`C:\Users\Ticket Operator\AppData\Local\Temp\ticket-pos-update-42.cmd`)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow {
		t.Fatal("update command must hide its console window")
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("update command must not create a console window")
	}
	if strings.Contains(strings.ToLower(command.SysProcAttr.CmdLine), " start ") {
		t.Fatalf("update command must not spawn a second command window: %s", command.SysProcAttr.CmdLine)
	}
	if !strings.Contains(command.SysProcAttr.CmdLine, `call "C:\Users\Ticket Operator\AppData\Local\Temp\ticket-pos-update-42.cmd"`) {
		t.Fatalf("update script path is not quoted: %s", command.SysProcAttr.CmdLine)
	}
}
