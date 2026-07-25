//go:build windows

package tray

import _ "embed"

//go:embed assets/venom.ico
var trayIcon []byte
