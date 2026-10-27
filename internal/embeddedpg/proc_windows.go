//go:build windows

package embeddedpg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procExtra is the kill-on-close job object the postmaster runs in. Its only
// handle is this process's, so a serve killed outright (Task Manager, a
// launcher's TerminateProcess, a crash) takes PostgreSQL and every backend
// with it instead of leaving an orphan holding the data directory.
type procExtra struct {
	job   windows.Handle
	token windows.Token
}

func binPath(binaries, program string) string {
	return filepath.Join(binaries, "bin", program+".exe")
}

// spawnAttrs is how the postmaster is started: its own process group (Setpgid
// on Unix, CREATE_NEW_PROCESS_GROUP|CREATE_NO_WINDOW on Windows), so a
// console Ctrl+C reaches serve and not PostgreSQL, and no console window
// appears for it.
func spawnAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
}

var (
	advapi32                  = windows.NewLazySystemDLL("advapi32.dll")
	procCreateRestrictedToken = advapi32.NewProc("CreateRestrictedToken")
)

const disableMaxPrivilege = 0x1

// restrictedToken is this process's token with every privilege removed and
// Administrators and Power Users made deny-only: postgres.exe refuses to run
// with administrative rights, which an elevated console would hand it. It is
// what pg_ctl itself does.
func restrictedToken() (windows.Token, error) {
	var current windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ADJUST_SESSIONID,
		&current); err != nil {
		return 0, err
	}
	defer current.Close()
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return 0, err
	}
	power, err := windows.CreateWellKnownSid(windows.WinBuiltinPowerUsersSid)
	if err != nil {
		return 0, err
	}
	disable := []windows.SIDAndAttributes{{Sid: admins}, {Sid: power}}
	var restricted windows.Token
	r, _, callErr := procCreateRestrictedToken.Call(
		uintptr(current), disableMaxPrivilege,
		uintptr(len(disable)), uintptr(unsafe.Pointer(&disable[0])),
		0, 0, 0, 0,
		uintptr(unsafe.Pointer(&restricted)))
	if r == 0 {
		return 0, fmt.Errorf("CreateRestrictedToken: %w", callErr)
	}
	return restricted, nil
}

func launch(proc *postmaster) error {
	if windows.GetCurrentProcessToken().IsElevated() {
		token, err := restrictedToken()
		if err != nil {
			return fmt.Errorf("this console runs as administrator and PostgreSQL refuses that; "+
				"a restricted token could not be made (%v): start Nodevas from a normal console", err)
		}
		proc.extra.token = token
		proc.cmd.SysProcAttr.Token = syscall.Token(token)
	}
	if err := proc.cmd.Start(); err != nil {
		if proc.extra.token != 0 {
			proc.extra.token.Close()
		}
		return err
	}
	proc.pid = proc.cmd.Process.Pid
	proc.extra.job = jobFor(proc.pid)
	go func() {
		proc.waitErr = proc.cmd.Wait()
		close(proc.done)
	}()
	return nil
}

// jobFor puts pid in a new kill-on-close job object, or returns 0 when that
// fails (the server then outlives a killed serve, and the next start
// recovers the orphan).
func jobFor(pid int) windows.Handle {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	return job
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied still means a process with that id exists.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

func killPID(pid int) {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	_ = windows.TerminateProcess(handle, 1)
}

// processImage is the executable's base name, "" when unknown.
func processImage(pid int) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}

// requestFastShutdown has pg_ctl signal the postmaster through its signal
// pipe: Windows has no SIGINT to send another process.
func requestFastShutdown(ctx context.Context, proc *postmaster) error {
	return run(ctx, proc.binaries, "pg_ctl", "stop", "-D", proc.dataDir, "-m", "fast", "-w", "-t", "10", "-s")
}

func immediateShutdown(proc *postmaster) {
	_ = run(context.Background(), proc.binaries, "pg_ctl", "stop", "-D", proc.dataDir, "-m", "immediate", "-w", "-t", "5", "-s")
}

func requestOrphanShutdown(ctx context.Context, binaries, dataDir string, pid int) {
	if binaries != "" {
		if run(ctx, binaries, "pg_ctl", "stop", "-D", dataDir, "-m", "fast", "-w", "-t", "10", "-s") == nil {
			return
		}
	}
	killPID(pid)
}

// releaseProc closes the job (ending anything left in it) and the token.
func releaseProc(proc *postmaster) {
	if proc == nil {
		return
	}
	if proc.extra.job != 0 {
		_ = windows.TerminateJobObject(proc.extra.job, 1)
		_ = windows.CloseHandle(proc.extra.job)
		proc.extra.job = 0
	}
	if proc.extra.token != 0 {
		proc.extra.token.Close()
		proc.extra.token = 0
	}
}

// ownerOnlyACL is a protected DACL granting the current user full control
// and nobody else anything; inherit makes it apply to a directory's
// contents too.
func ownerOnlyACL(inherit bool) (*windows.ACL, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if inherit {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	return windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
}

func setOwnerOnly(path string, inherit bool) error {
	acl, err := ownerOnlyACL(inherit)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// WriteSecretFile writes data readable by the current user only: mode 0600 on
// Unix, a protected DACL naming only the current user's SID on Windows. The
// file is written beside the target, locked down, then renamed over it, so a
// looser file already there is replaced rather than reused.
func WriteSecretFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secret-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := setOwnerOnly(name, false); err != nil {
		return fmt.Errorf("embeddedpg: restrict %s: %w", path, err)
	}
	if err := os.WriteFile(name, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("embeddedpg: write %s: %w", path, err)
	}
	return nil
}

// restrictDir gives a directory an inheritable owner-only DACL.
func restrictDir(dir string) error {
	if err := setOwnerOnly(dir, true); err != nil && !strings.Contains(err.Error(), "Access is denied") {
		return err
	}
	return nil
}

func refuseRoot() error { return nil }
