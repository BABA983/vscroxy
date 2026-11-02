package control

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createPrivateTemp(dir string) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid := user.User.Sid.String()
	if sid == "" {
		return nil, errors.New("cannot identify discovery owner")
	}
	// Protect the file from the instant it exists. Tightening an inherited DACL
	// after CreateTemp would leave a window in which someone could open the
	// empty file and retain a readable handle when token bytes are later written.
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;FA;;;" + sid + ")")
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	for range 10 {
		path := filepath.Join(dir, ".control-"+rand.Text())
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			&sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if err != nil {
			return nil, err
		}
		f := os.NewFile(uintptr(handle), path)
		info, err := f.Stat()
		// Fail closed on filesystems that ignore or cannot represent the ACL.
		if err != nil || !privateFile(f, info) {
			f.Close()
			os.Remove(path)
			return nil, errors.New("cannot verify private discovery protection")
		}
		return f, nil
	}
	return nil, errors.New("cannot allocate private discovery file")
}

func privateFile(f *os.File, _ os.FileInfo) bool {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	// Query the opened file, never its pathname: Load has already checked file
	// identity and must not validate an ACL belonging to a replacement path.
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	runtime.KeepAlive(f)
	return err == nil && privateWindowsDescriptor(sd, user.User.Sid)
}

func privateWindowsDescriptor(sd *windows.SECURITY_DESCRIPTOR, user *windows.SID) bool {
	if sd == nil || !sd.IsValid() {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user) {
		return false
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	dacl, defaulted, err := sd.DACL()
	// Accept only one explicit, non-inheritable allow ACE for the current user.
	// Unknown/object/callback ACEs, deny-based policies, NULL/empty DACLs and
	// extra principals fail closed rather than guessing at effective access.
	if err != nil || defaulted || dacl == nil || dacl.AceCount != 1 {
		return false
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil {
		return false
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || ace.Mask&windows.FILE_READ_DATA == 0 {
		return false
	}
	const sidOffset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
	if uintptr(ace.Header.AceSize) < sidOffset+8 {
		return false
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	return sid.IsValid() && uintptr(sid.Len()) <= uintptr(ace.Header.AceSize)-sidOffset && sid.Equals(user)
}
