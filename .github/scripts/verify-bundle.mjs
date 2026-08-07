// Post-build guard for `task bundle`: proves the artifact it just produced is
// a GUI-subsystem binary, then sweeps the linker's leftovers out of dist/.
//
// Why this exists: on 2026-08-06 the owner double-clicked dist/venom.exe and got
// a Windows Terminal window. That artifact measured subsystem 3 — it had been
// produced by a plain `go build` instead of `task bundle`'s
// `-ldflags "-H windowsgui"`. Nothing could have caught it: dist/ is
// .gitignore'd, so CI never sees the shipped binary. The only place the check
// can live is the build itself.
//
// Exit codes: 0 = verified GUI, 1 = wrong subsystem or unreadable artifact.
// A leftover that cannot be deleted (a running instance holds it) is a WARNING,
// never a failure.

import { readFileSync, readdirSync, unlinkSync } from "node:fs";
import { join } from "node:path";

import { PE_SUBSYSTEM_GUI, describe, subsystemOf } from "./pe-subsystem.mjs";

const DIST = "dist";
const ARTIFACT = join(DIST, "venom.exe");

function fail(message) {
  console.error(`verify-bundle: FAIL — ${message}`);
  process.exit(1);
}

let image;
try {
  image = readFileSync(ARTIFACT);
} catch (err) {
  fail(`cannot read ${ARTIFACT}: ${err.message}`);
}

let header;
try {
  header = subsystemOf(image);
} catch (err) {
  fail(`${ARTIFACT}: ${err.message}`);
}

if (header.subsystem !== PE_SUBSYSTEM_GUI) {
  fail(
    `${ARTIFACT} is ${describe(header.subsystem)}.\n` +
      `  The shipped tray binary must be linked with -ldflags "-H windowsgui" so a\n` +
      `  double-click opens no terminal window. Build it with \`task bundle\`, never\n` +
      `  with a plain \`go build -o dist/venom.exe\`.`,
  );
}

console.log(`verify-bundle: ${ARTIFACT} is ${describe(header.subsystem)}, magic 0x${header.magic.toString(16)} — OK`);

// The Go linker renames a locked output to "<name>~" and writes a fresh file, so
// a venom.exe~ in dist/ means a build ran over a live tray; it stays locked until
// that instance quits. venom-*.exe are ad-hoc experiment builds.
const leftovers = readdirSync(DIST).filter((name) => name.endsWith(".exe~") || /^venom-.*\.exe$/.test(name));
for (const name of leftovers) {
  try {
    unlinkSync(join(DIST, name));
    console.log(`verify-bundle: removed leftover ${join(DIST, name)}`);
  } catch (err) {
    console.warn(`verify-bundle: WARNING — could not remove ${join(DIST, name)} (${err.code ?? err.message}); a running instance still holds it, quit the tray to release it`);
  }
}
