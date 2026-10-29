//go:build linux && !amd64 && !arm64

package restore

// sysRenameat2 is unknown here: renameat2 reports ENOSYS and restores take
// the mkdir path (see webspace.place).
const sysRenameat2 = -1
