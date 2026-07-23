#!/usr/bin/env node
// Vendors the pinned Lucide SVG subset actually referenced by icons/icons.css from the
// `lucide-static` devDependency into assets/icons/, then rewrites icons/icons.css to
// reference the local files instead of unpkg. Deterministic, offline-safe once run —
// re-run after adding a new glyph reference.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, "..");
const ICONS_CSS = path.join(ROOT, "icons", "icons.css");
const ASSETS_DIR = path.join(ROOT, "assets", "icons");
const LUCIDE_DIR = path.join(ROOT, "node_modules", "lucide-static", "icons");

function main() {
  if (!fs.existsSync(LUCIDE_DIR)) {
    console.error("lucide-static not found in node_modules — run `npm install` first.");
    process.exit(1);
  }
  const lucidePkg = JSON.parse(fs.readFileSync(path.join(ROOT, "node_modules", "lucide-static", "package.json"), "utf8"));
  const LUCIDE_VERSION = lucidePkg.version;

  const css = fs.readFileSync(ICONS_CSS, "utf8");
  const refs = [...css.matchAll(/url\((?:https:\/\/unpkg\.com\/lucide-static@[\d.]+\/icons\/|\.\.\/assets\/icons\/)([a-z0-9-]+)\.svg\)/g)];
  const glyphs = new Set(refs.map((m) => m[1]));
  if (!glyphs.size) {
    console.error("No glyph references found in icons/icons.css — nothing to vendor.");
    process.exit(1);
  }

  fs.mkdirSync(ASSETS_DIR, { recursive: true });
  const missing = [];
  let copied = 0;
  for (const glyph of glyphs) {
    const src = path.join(LUCIDE_DIR, glyph + ".svg");
    const dest = path.join(ASSETS_DIR, glyph + ".svg");
    if (!fs.existsSync(src)) {
      missing.push(glyph);
      continue;
    }
    fs.copyFileSync(src, dest);
    copied++;
  }
  if (missing.length) {
    console.error("Missing glyphs in lucide-static@" + LUCIDE_VERSION + ": " + missing.join(", "));
    process.exit(1);
  }

  // Remove stale vendored files no longer referenced.
  const existing = fs.readdirSync(ASSETS_DIR).filter((f) => f.endsWith(".svg"));
  let removed = 0;
  for (const f of existing) {
    const name = f.replace(/\.svg$/, "");
    if (!glyphs.has(name)) {
      fs.unlinkSync(path.join(ASSETS_DIR, f));
      removed++;
    }
  }

  const rewritten = css.replace(
    /https:\/\/unpkg\.com\/lucide-static@[\d.]+\/icons\/([a-z0-9-]+)\.svg/g,
    "../assets/icons/$1.svg"
  );
  fs.writeFileSync(ICONS_CSS, rewritten);

  console.log(
    "VENDOR-ICONS: " + copied + " glyph(s) vendored from lucide-static@" + LUCIDE_VERSION +
    " into assets/icons/ (" + removed + " stale file(s) removed). icons/icons.css rewritten to local paths."
  );
}

main();
