package restore

// sysRenameat2 is renameat2(2) on linux/arm64, which uses the generic
// system call table (382 is 32-bit arm's number, not arm64's).
const sysRenameat2 = 276
