//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	HKEY_CURRENT_USER = 0x80000001
	KEY_QUERY_VALUE   = 0x0001
	KEY_SET_VALUE     = 0x0002
	REG_SZ            = 1
	regRunPath        = `Software\Microsoft\Windows\CurrentVersion\Run`
	regValueName      = "NonoDeskpet"
)

type HKEY uintptr

var (
	advapi32          = syscall.NewLazyDLL("advapi32.dll")
	pRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	pRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	pRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	pRegDeleteValueW  = advapi32.NewProc("RegDeleteValueW")
	pRegCloseKey      = advapi32.NewProc("RegCloseKey")
)

func openRunKey(access uintptr) (HKEY, bool) {
	var hk HKEY
	r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER,
		uintptr(unsafe.Pointer(utf16ptr(regRunPath))), 0, access,
		uintptr(unsafe.Pointer(&hk)))
	return hk, r == 0
}

func autoStartEnabled() bool {
	hk, ok := openRunKey(KEY_QUERY_VALUE)
	if !ok {
		return false
	}
	defer pRegCloseKey.Call(uintptr(hk))
	buf := make([]uint16, 512)
	size := uint32(len(buf) * 2)
	var typ uint32
	r, _, _ := pRegQueryValueExW.Call(uintptr(hk),
		uintptr(unsafe.Pointer(utf16ptr(regValueName))), 0,
		uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	return r == 0
}

func setAutoStart(on bool) {
	hk, ok := openRunKey(KEY_SET_VALUE)
	if !ok {
		return
	}
	defer pRegCloseKey.Call(uintptr(hk))
	if !on {
		pRegDeleteValueW.Call(uintptr(hk), uintptr(unsafe.Pointer(utf16ptr(regValueName))))
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	val := `"` + exe + `"`
	wt, _ := syscall.UTF16FromString(val)
	pRegSetValueExW.Call(uintptr(hk), uintptr(unsafe.Pointer(utf16ptr(regValueName))),
		0, REG_SZ, uintptr(unsafe.Pointer(&wt[0])), uintptr(len(wt)*2))
}
