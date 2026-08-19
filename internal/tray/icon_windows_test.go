//go:build windows

package tray

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// wantIconSizes are the square pixel sizes the redesigned icon must carry
// (design 2026-07-31): small tray/taskbar sizes through 64px.
var wantIconSizes = []int{16, 20, 24, 32, 48, 64}

var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// TestTrayIcon_IsPNGCompressedICOWithExpectedSizes parses the embedded ICO
// by hand (ICONDIR + ICONDIRENTRY are trivially small structures) and
// asserts entry count, per-entry square dimensions, and that every payload
// is a PNG (the generator writes PNG-compressed entries).
func TestTrayIcon_IsPNGCompressedICOWithExpectedSizes(t *testing.T) {
	if len(trayIcon) < 6 {
		t.Fatalf("embedded icon too small: %d bytes", len(trayIcon))
	}
	if rt := binary.LittleEndian.Uint16(trayIcon[2:4]); rt != 1 {
		t.Fatalf("ICONDIR type = %d, want 1 (icon)", rt)
	}
	count := int(binary.LittleEndian.Uint16(trayIcon[4:6]))
	if count != len(wantIconSizes) {
		t.Fatalf("icon entry count = %d, want %d", count, len(wantIconSizes))
	}
	for i := 0; i < count; i++ {
		e := trayIcon[6+16*i : 6+16*i+16]
		w, h := int(e[0]), int(e[1])
		if w != wantIconSizes[i] || h != wantIconSizes[i] {
			t.Errorf("entry %d = %dx%d, want %dx%d", i, w, h, wantIconSizes[i], wantIconSizes[i])
		}
		size := int(binary.LittleEndian.Uint32(e[8:12]))
		off := int(binary.LittleEndian.Uint32(e[12:16]))
		if off+size > len(trayIcon) {
			t.Fatalf("entry %d payload [%d:%d] out of bounds (%d)", i, off, off+size, len(trayIcon))
		}
		if !bytes.HasPrefix(trayIcon[off:off+size], pngMagic) {
			t.Errorf("entry %d payload is not PNG-compressed", i)
		}
	}
}

func TestVenomIconResourceForExe_UsesEmbeddedResourceOne(t *testing.T) {
	got := venomIconResourceForExe(`C:\Users\hamee\Desktop\venom-router\dist\venom.exe`)
	want := `C:\Users\hamee\Desktop\venom-router\dist\venom.exe,-1`
	if got != want {
		t.Fatalf("resource = %q, want %q", got, want)
	}
}
