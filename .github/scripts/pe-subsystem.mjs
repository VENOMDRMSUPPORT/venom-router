// Reads the Subsystem field out of a Windows PE image.
//
// This exists because the difference between the shipped tray binary and a
// developer console build is invisible in the filesystem and shows up only when
// the owner double-clicks it: subsystem 3 (CONSOLE) makes Windows allocate a
// terminal window, which the tray cannot hide when the host is Windows Terminal
// (see internal/tray/winapi_windows.go). `task bundle` links -H windowsgui to
// get subsystem 2 (GUI); verify-bundle.mjs uses this reader to prove it did.

export const PE_SUBSYSTEM_CONSOLE = 3;
export const PE_SUBSYSTEM_GUI = 2;

const E_LFANEW_OFFSET = 0x3c; // DOS header field pointing at the PE header
const PE_SIGNATURE = 0x00004550; // "PE\0\0"
const COFF_HEADER_SIZE = 0x18; // signature + COFF file header, i.e. where the optional header starts
const SUBSYSTEM_OFFSET = 68; // into the optional header; identical for PE32 (0x10b) and PE32+ (0x20b)

/**
 * @param {Buffer} buf the whole image (or at least its headers)
 * @returns {{magic: number, subsystem: number}}
 */
export function subsystemOf(buf) {
  if (buf.length < E_LFANEW_OFFSET + 4) {
    throw new Error(`not a PE image: file is too small (${buf.length} bytes) to hold e_lfanew`);
  }
  const peOffset = buf.readInt32LE(E_LFANEW_OFFSET);
  const subsystemAt = peOffset + COFF_HEADER_SIZE + SUBSYSTEM_OFFSET;
  if (peOffset < 0 || subsystemAt + 2 > buf.length) {
    throw new Error(`not a PE image: truncated before the Subsystem field (e_lfanew=0x${peOffset.toString(16)}, size=${buf.length})`);
  }
  const signature = buf.readUInt32LE(peOffset);
  if (signature !== PE_SIGNATURE) {
    throw new Error(`bad PE signature 0x${signature.toString(16)} at 0x${peOffset.toString(16)}, want 0x${PE_SIGNATURE.toString(16)}`);
  }
  return {
    magic: buf.readUInt16LE(peOffset + COFF_HEADER_SIZE),
    subsystem: buf.readUInt16LE(subsystemAt),
  };
}

/** describe turns a subsystem number into the name used in messages. */
export function describe(subsystem) {
  if (subsystem === PE_SUBSYSTEM_GUI) return "GUI (2) — silent, no console window";
  if (subsystem === PE_SUBSYSTEM_CONSOLE) return "CONSOLE (3) — Windows allocates a terminal window";
  return `unknown (${subsystem})`;
}
