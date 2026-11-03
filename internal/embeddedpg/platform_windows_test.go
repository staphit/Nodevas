//go:build windows

package embeddedpg

import (
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func processAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

func assertOwnGroup(t *testing.T, attr *syscall.SysProcAttr) {
	t.Helper()
	if attr == nil {
		t.Fatal("no SysProcAttr")
	}
	if attr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Errorf("CreationFlags %#x lacks CREATE_NEW_PROCESS_GROUP: a console Ctrl+C would reach PostgreSQL", attr.CreationFlags)
	}
	if attr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Errorf("CreationFlags %#x lacks CREATE_NO_WINDOW", attr.CreationFlags)
	}
}

// assertOwnerOnly checks the file's DACL is protected and grants access to
// the current user's SID alone.
func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	info, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo: %v", err)
	}
	control, _, err := info.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Error("DACL is not protected: inherited ACEs still apply")
	}
	dacl, _, err := info.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("DACL: %v", err)
	}
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if dacl.AceCount == 0 {
		t.Fatal("empty DACL")
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(user.User.Sid) {
			t.Errorf("ACE %d grants %s, not the current user %s", i, sid, user.User.Sid)
		}
	}
}
