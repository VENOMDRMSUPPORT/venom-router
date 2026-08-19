//go:build windows

package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32                           = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	procCoUninitialize              = ole32.NewProc("CoUninitialize")
	procSHGetPropertyStoreForWindow = shell32.NewProc("SHGetPropertyStoreForWindow")
)

const (
	coInitApartmentThreaded = 0x2
	coInitDisableOle1DDE    = 0x4
	rpcEChangedMode         = 0x80010106
)

var (
	iidPropertyStore = windows.GUID{
		Data1: 0x886D8EEB,
		Data2: 0x8CF2,
		Data3: 0x4446,
		Data4: [8]byte{0x8D, 0x02, 0xCD, 0xBA, 0x1D, 0xBD, 0xCF, 0x99},
	}
	appUserModelID = "Venom.Router.ControlWindow"
)

type propertyKey struct {
	fmtid windows.GUID
	pid   uint32
}

type propVariant struct {
	vt       uint16
	reserved [3]uint16
	value    uintptr
}

type propertyStore struct {
	lpVtbl *propertyStoreVtbl
}

type propertyStoreVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	GetCount       uintptr
	GetAt          uintptr
	GetValue       uintptr
	SetValue       uintptr
	Commit         uintptr
}

func (s *propertyStore) vtable() *propertyStoreVtbl {
	return s.lpVtbl
}

func (s *propertyStore) Release() {
	if s == nil || s.lpVtbl == nil {
		return
	}
	_, _, _ = syscall.SyscallN(s.vtable().Release, uintptr(unsafe.Pointer(s)))
}

func (s *propertyStore) SetValue(key propertyKey, value *propVariant) error {
	hr, _, _ := syscall.SyscallN(s.vtable().SetValue,
		uintptr(unsafe.Pointer(s)),
		uintptr(unsafe.Pointer(&key)),
		uintptr(unsafe.Pointer(value)))
	if hr != 0 {
		return windows.Errno(hr)
	}
	return nil
}

func (s *propertyStore) Commit() error {
	hr, _, _ := syscall.SyscallN(s.vtable().Commit, uintptr(unsafe.Pointer(s)))
	if hr != 0 {
		return windows.Errno(hr)
	}
	return nil
}

// applyVenomWindowIdentity rebrands the browser window with the app's own taskbar
// identity. Chromium still renders the page, but Windows now sees this window as
// Venom instead of as a generic browser surface.
func applyVenomWindowIdentity(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	defer applyVenomWindowIcon(hwnd)

	initialized, ok := coInitializeForPropertyStore()
	if !ok {
		return
	}
	if initialized {
		defer func() { _, _, _ = procCoUninitialize.Call() }()
	}

	store, err := getWindowPropertyStore(hwnd)
	if err != nil {
		return
	}
	defer store.Release()

	if iconResource, err := venomIconResource(); err == nil {
		_ = store.SetValue(propertyKey{
			fmtid: iidAppUserModel,
			pid:   3, // System.AppUserModel.RelaunchIconResource
		}, newStringPropVariant(iconResource))
	}
	_ = store.SetValue(propertyKey{
		fmtid: iidAppUserModel,
		pid:   5, // System.AppUserModel.ID
	}, newStringPropVariant(appUserModelID))
	_ = store.Commit()
}

func coInitializeForPropertyStore() (shouldUninitialize bool, ok bool) {
	hr, _, _ := procCoInitializeEx.Call(0, coInitApartmentThreaded|coInitDisableOle1DDE)
	switch hr {
	case 0, 1:
		return true, true
	case rpcEChangedMode:
		return false, true
	default:
		return false, false
	}
}

func getWindowPropertyStore(hwnd uintptr) (*propertyStore, error) {
	var store *propertyStore
	hr, _, _ := procSHGetPropertyStoreForWindow.Call(
		hwnd,
		uintptr(unsafe.Pointer(&iidPropertyStore)),
		uintptr(unsafe.Pointer(&store)),
	)
	if hr != 0 {
		return nil, windows.Errno(hr)
	}
	return store, nil
}

func newStringPropVariant(value string) *propVariant {
	ptr, _ := syscall.UTF16PtrFromString(value)
	return &propVariant{
		vt:    0x1f, // VT_LPWSTR
		value: uintptr(unsafe.Pointer(ptr)),
	}
}

func venomIconResource() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return venomIconResourceForExe(exe), nil
}

func venomIconResourceForExe(exe string) string {
	return fmt.Sprintf("%s,-1", filepath.Clean(exe))
}

var iidAppUserModel = windows.GUID{
	Data1: 0x9F4C2855,
	Data2: 0x9F79,
	Data3: 0x4B39,
	Data4: [8]byte{0xA8, 0xD0, 0xE1, 0xD4, 0x2D, 0xE1, 0xD5, 0xF3},
}
