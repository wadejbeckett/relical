//go:build linux

package restore

import (
	"syscall"
	"unsafe"
)

// renameNoReplace is RENAME_NOREPLACE: renameat2 fails with EEXIST rather
// than replace the destination.
const renameNoReplace = 1

// rawRenameat2 is renameat2(olddirfd, oldpath, newdirfd, newpath, flags).
// It never follows a symlink in the last component of either path, and
// with renameNoReplace never replaces anything. Kernels before 3.15, and
// file systems without the flag, answer ENOSYS or EINVAL.
func rawRenameat2(olddirfd int, oldpath string, newdirfd int, newpath string, flags uint) error {
	if sysRenameat2 < 0 {
		return syscall.ENOSYS
	}
	oldp, err := syscall.BytePtrFromString(oldpath)
	if err != nil {
		return err
	}
	newp, err := syscall.BytePtrFromString(newpath)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(uintptr(sysRenameat2), uintptr(olddirfd), uintptr(unsafe.Pointer(oldp)),
		uintptr(newdirfd), uintptr(unsafe.Pointer(newp)), uintptr(flags), 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// renameat2 is rawRenameat2; a variable so that tests can simulate an old
// kernel or watch the move.
var renameat2 = rawRenameat2
