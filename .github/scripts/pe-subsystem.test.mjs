// Tests for the PE subsystem reader that guards `task bundle`. Run by
// `node --test .github/scripts/`, which `task bundle` invokes before trusting
// the reader's verdict on a real binary.
//
// The fixtures are synthesised rather than read from disk: the point is to pin
// the header arithmetic (which is what silently reads the wrong field), and a
// checked-in .exe fixture would be both large and gitignored.

import { test } from "node:test";
import assert from "node:assert/strict";

import { PE_SUBSYSTEM_GUI, PE_SUBSYSTEM_CONSOLE, subsystemOf } from "./pe-subsystem.mjs";

// fakePE builds the smallest byte sequence that is shaped like a PE image:
// the e_lfanew pointer at 0x3C, the "PE\0\0" signature, the COFF header, then
// the optional header whose 69th..70th bytes hold Subsystem.
function fakePE({ peOffset = 0xf0, magic = 0x20b, subsystem = PE_SUBSYSTEM_GUI, signature = 0x00004550 } = {}) {
  const buf = Buffer.alloc(peOffset + 0x18 + 96, 0);
  buf.writeInt32LE(peOffset, 0x3c);
  buf.writeUInt32LE(signature, peOffset);
  buf.writeUInt16LE(magic, peOffset + 0x18);
  buf.writeUInt16LE(subsystem, peOffset + 0x18 + 68);
  return buf;
}

test("reads the GUI subsystem of a PE32+ image", () => {
  assert.deepEqual(subsystemOf(fakePE({ magic: 0x20b, subsystem: PE_SUBSYSTEM_GUI })), {
    magic: 0x20b,
    subsystem: PE_SUBSYSTEM_GUI,
  });
});

test("reads the CONSOLE subsystem — the value that must fail the bundle", () => {
  assert.equal(subsystemOf(fakePE({ subsystem: PE_SUBSYSTEM_CONSOLE })).subsystem, PE_SUBSYSTEM_CONSOLE);
});

test("the Subsystem offset is the same for PE32 and PE32+", () => {
  // A 32-bit optional header differs from PE32+ AFTER Subsystem, so reading it
  // at a magic-dependent offset would be a silent, plausible-looking bug.
  assert.equal(subsystemOf(fakePE({ magic: 0x10b, subsystem: PE_SUBSYSTEM_GUI })).subsystem, PE_SUBSYSTEM_GUI);
});

test("honours e_lfanew instead of assuming a fixed header offset", () => {
  assert.equal(subsystemOf(fakePE({ peOffset: 0x200, subsystem: PE_SUBSYSTEM_CONSOLE })).subsystem, PE_SUBSYSTEM_CONSOLE);
});

test("rejects a file whose PE signature is missing", () => {
  assert.throws(() => subsystemOf(fakePE({ signature: 0x0000dead })), /PE signature/);
});

test("rejects a file truncated before the Subsystem field", () => {
  const full = fakePE();
  assert.throws(() => subsystemOf(full.subarray(0, 0xf0 + 0x18 + 40)), /too small|truncated/i);
});

test("rejects a buffer too small to hold e_lfanew", () => {
  assert.throws(() => subsystemOf(Buffer.alloc(8)), /too small|truncated/i);
});
