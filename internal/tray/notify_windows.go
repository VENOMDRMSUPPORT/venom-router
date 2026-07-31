package tray

import "golang.org/x/sys/windows"

// NotifyStartupFailure shows a blocking error MessageBox so a bare tray
// mode boot failure is VISIBLE to a double-click user. Without this, a
// failed boot (e.g. a corrupt keyring) kills the process before the tray
// icon exists and the owner sees nothing at all. Nil (0) owner window —
// there is no window yet; MB_SETFOREGROUND keeps the box from opening
// buried behind other windows. Failures to show the box are ignored:
// this is a last-resort notification on an already-failing path.
func NotifyStartupFailure(detail string) {
	text, err := windows.UTF16PtrFromString("Venom Router failed to start:\n\n" + detail)
	if err != nil {
		return
	}
	caption, err := windows.UTF16PtrFromString("Venom Router")
	if err != nil {
		return
	}
	_, _ = windows.MessageBox(0, text, caption,
		windows.MB_OK|windows.MB_ICONERROR|windows.MB_SETFOREGROUND)
}
