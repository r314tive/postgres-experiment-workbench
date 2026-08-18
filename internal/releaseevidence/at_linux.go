//go:build linux

package releaseevidence

import (
	"runtime"
	"syscall"
	"unsafe"
)

func openAt(directoryFD int, name string, flags int, mode uint32) (int, error) {
	return syscall.Openat(directoryFD, name, flags, mode)
}

func linkAt(directoryFD int, oldName, newName string) error {
	oldPointer, err := syscall.BytePtrFromString(oldName)
	if err != nil {
		return err
	}
	newPointer, err := syscall.BytePtrFromString(newName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(
		syscall.SYS_LINKAT,
		uintptr(directoryFD),
		uintptr(unsafe.Pointer(oldPointer)),
		uintptr(directoryFD),
		uintptr(unsafe.Pointer(newPointer)),
		0,
		0,
	)
	runtime.KeepAlive(oldPointer)
	runtime.KeepAlive(newPointer)
	if errno != 0 {
		return errno
	}
	return nil
}

func unlinkAt(directoryFD int, name string) error {
	return syscall.Unlinkat(directoryFD, name)
}
