package main

import (
	"strings"
	"testing"
)

func TestParseInvite(t *testing.T) {
	tok := strings.Repeat("aZ0_-", 8) + "abc" // 43 chars
	for _, name := range []string{
		`C:\Users\a\Downloads\ManashaktiSupport-Setup-` + tok + `.exe`,
		`C:\Users\a\Downloads\ManashaktiSupport-Setup-` + tok + ` (1).exe`,
		`C:\Users\a\Downloads\manashaktisupport-setup-` + tok + `.exe`,
	} {
		mode, got, err := parseInvite(name)
		if err != nil || mode != "setup" || got != tok {
			t.Fatalf("%s: %q %q %v", name, mode, got, err)
		}
	}
	if m, _, err := parseInvite(`ManashaktiSupport-Help-` + tok + `.exe`); err != nil || m != "help" {
		t.Fatalf("help: %q %v", m, err)
	}
	if _, _, err := parseInvite(`invite-helper.exe`); err == nil {
		t.Fatal("renamed file accepted")
	}
}

func TestLastIDIgnoresPackerOutput(t *testing.T) {
	out := []byte("is windows7: false\r\nexecuting C:\\x\\ManashaktiSupport.exe\r\n211867013\r\n")
	if id := lastID(out); id != "211867013" {
		t.Fatalf("got %q", id)
	}
	if id := lastID([]byte("Installation and administrative privileges required!\n")); id != "" {
		t.Fatalf("got %q", id)
	}
}

func TestRandomPassword(t *testing.T) {
	pw, err := randomPassword(24)
	if err != nil || len(pw) != 24 || strings.Trim(pw, pwAlphabet) != "" {
		t.Fatalf("%q %v", pw, err)
	}
}

func TestMsiFailureContext(t *testing.T) {
	log := "MSI (s) (A0:B4) start\r\nCustomAction TerminateProcesses returned actual error code 1603\r\nAction ended 21:50: InstallFinalize. Return value 3.\r\nmore\r\n"
	// UTF-16LE with BOM, as msiexec usually writes it
	u := []byte{0xFF, 0xFE}
	for _, r := range log {
		u = append(u, byte(r), byte(r>>8))
	}
	for _, raw := range [][]byte{[]byte(log), u} {
		got := msiFailureContext(raw)
		if !strings.Contains(got, "TerminateProcesses returned actual error code 1603") || !strings.Contains(got, "Return value 3") || strings.Contains(got, "more") {
			t.Fatalf("got %q", got)
		}
	}
	if msiFailureContext([]byte("all fine\r\n")) != "" {
		t.Fatal("context for successful log")
	}
}
