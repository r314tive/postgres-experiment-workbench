//go:build darwin

package releaseevidence

import (
	"runtime"
	"syscall"
	"unsafe"
)

const (
	darwinSysOpenat   = 463
	darwinSysLinkat   = 471
	darwinSysUnlinkat = 472
)

func openAt(directoryFD int, name string, flags int, mode uint32) (int, error) {
	namePointer, err := syscall.BytePtrFromString(name)
	if err != nil {
		return -1, err
	}
	fd, _, errno := syscall.Syscall6(
		darwinSysOpenat,
		uintptr(directoryFD),
		uintptr(unsafe.Pointer(namePointer)),
		uintptr(flags),
		uintptr(mode),
		0,
		0,
	)
	runtime.KeepAlive(namePointer)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
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
		darwinSysLinkat,
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
	namePointer, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall(
		darwinSysUnlinkat,
		uintptr(directoryFD),
		uintptr(unsafe.Pointer(namePointer)),
		0,
	)
	runtime.KeepAlive(namePointer)
	if errno != 0 {
		return errno
	}
	return nil
}
