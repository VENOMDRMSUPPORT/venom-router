// Venom DS — guardrail gates:
//  1. no-raw-color lint (component sources must consume tokens) — full authored UI surface
//  2. prohibited terminology scan (legacy DS, fake tiers, vague errors, no `rejected` cert state, no stored `expired` reservation)
//  3. secret-pattern canary (no realistic credentials in fixtures)
//  4. icon-map completeness (every referenced glyph exists in icons/icons.css)
//  5. domain-state coverage (every required state literal is rendered somewhere in states/ or components/)
//  6. story coverage (every component directory ships a card; every mandated state matrix exists)
//  7. forbidden runtime CDN URLs (offline packaging contract)
//  8. required-component presence (docs/07 mandated additions)
//  9. no `any` in the public component API (.tsx source + generated .d.ts)
// Throws on any violation; returns a summary object.
async function checkVenomGuardrails(io) {
  const { readFile, ls, log } = io;

  const COMPONENT_DIRS = ['components/icons', 'components/actions', 'components/forms', 'components/display', 'components/feedback', 'components/navigation', 'components/containers', 'components/overlay', 'components/data', 'components/domain-provider', 'components/domain-model', 'components/domain-quota', 'components/domain-routing', 'components/domain-security', 'components/domain-diagnostics'];
  const UI_SCOPE_DIRS = COMPONENT_DIRS.concat(['states', 'foundations', 'storybook', 'ui_kits/venom-console', 'css', 'icons']);

  async function filesIn(dir, exts) {
    const out = [];
    for (const f of await ls(dir)) if (exts.some((e) => f.endsWith(e))) out.push(dir + '/' + f);
    return out;
  }
  const errors = [];

  // ---- 1. raw-color lint — full authored UI surface: components (incl. generated .entry.tsx
  // demo bootstraps), foundations specimens, state matrices, storybook hub, ui_kit
  // compositions, and hand-authored CSS. Generated theme/token CSS is exempt by construction
  // (it's a compiled var() target, never a literal source).
  const lintFiles = [];
  for (const d of UI_SCOPE_DIRS) lintFiles.push(...await filesIn(d, ['.tsx', '.html', '.jsx']));
  lintFiles.push('css/components-core.css', 'css/components-domain.css', 'tokens/base.css', 'icons/icons.css');
  let lintCount = 0;
  for (const f of lintFiles) {
    const src = await readFile(f);
    lintCount++;
    const hex = src.match(/#[0-9a-fA-F]{3,8}\b/g);
    const rgb = src.match(/\brgba?\(/g);
    if (hex) errors.push('raw-color: ' + f + ' contains hex literal(s): ' + hex.slice(0, 3).join(', '));
    if (rgb) errors.push('raw-color: ' + f + ' contains rgb()/rgba() literal(s)');
  }

  // ---- 2. prohibited terminology (UI-facing scopes only; guides that DOCUMENT the bans are exempt)
  const FORBIDDEN = ['venom/standard', 'venom/plus', 'Hex AIOS', 'hex-aios', 'HexAIOS', 'Something went wrong', 'Oops!'];
  let termFiles = 0;
  for (const d of UI_SCOPE_DIRS) {
    for (const f of await filesIn(d, ['.tsx', '.html', '.jsx', '.css', '.d.ts'])) {
      const src = await readFile(f);
      termFiles++;
      for (const w of FORBIDDEN) if (src.includes(w)) errors.push('terminology: "' + w + '" found in ' + f);
    }
  }
  // `rejected` must not exist as a certification state; reservations must not store `expired`.
  const cert = await readFile('components/domain-model/ModelIntelligence.tsx');
  const certMap = cert.match(/const CERT[^;]*?\{([\s\S]*?)\n\};/);
  if (!certMap || /rejected/.test(certMap[1])) errors.push('state-machine: certification map missing or contains a rejected state');
  const quota = await readFile('components/domain-quota/Quota.tsx');
  const resMap = quota.match(/const RESERVATION[^;]*?\{([\s\S]*?)\n\};/);
  if (!resMap || /\bexpired\b/.test(resMap[1])) errors.push('state-machine: reservation map missing or contains a stored expired state');
  for (const key of ['reserved', 'reconciliation_pending', 'settled', 'released', 'unknown_consumption']) {
    if (resMap && !resMap[1].includes(key)) errors.push('state-machine: reservation map missing ' + key);
  }

  // ---- 3. secret canary
  const SECRET_RES = [/\bsk-[A-Za-z0-9-]{24,}/g, /\bghp_[A-Za-z0-9]{30,}\b/g, /\bgithub_pat_[A-Za-z0-9_]{30,}\b/g, /\bAKIA[0-9A-Z]{16}\b/g, /\bvk_live_[A-Za-z0-9-]{20,}/g, /\beyJ[A-Za-z0-9_-]{40,}\b/g];
  const SAFE = /EXAMPLE|FIXTURE|not-real|not_a_real|•/;
  for (const d of UI_SCOPE_DIRS.concat(['patterns', 'accessibility', 'validation'])) {
    let names = [];
    try { names = await filesIn(d, ['.tsx', '.html', '.jsx', '.md', '.json', '.css', '.ts']); } catch (e) { continue; }
    for (const f of names) {
      if (f.endsWith('check-guardrails.cjs') || f.endsWith('check-guardrails.js')) continue;
      const src = await readFile(f);
      for (const re of SECRET_RES) {
        const m = src.match(re);
        if (m) for (const hit of m) if (!SAFE.test(hit) && !SAFE.test(src.slice(Math.max(0, src.indexOf(hit) - 40), src.indexOf(hit) + hit.length + 40))) {
          errors.push('secret-canary: realistic credential-like string in ' + f + ': ' + hit.slice(0, 12) + '…');
        }
      }
    }
  }

  // ---- 4. icon-map completeness
  const iconsCss = await readFile('icons/icons.css');
  const defined = new Set([...iconsCss.matchAll(/\.vn-icon--([a-z0-9-]+)\s*\{\s*-webkit-mask/g)].map((m) => m[1]));
  const used = new Set();
  for (const d of UI_SCOPE_DIRS) {
    for (const f of await filesIn(d, ['.tsx', '.html', '.jsx'])) {
      const src = await readFile(f);
      for (const m of src.matchAll(/vn-icon--([a-z0-9-]+)/g)) if (!['sm', 'lg', 'xl'].includes(m[1])) used.add(m[1]);
    }
  }
  const iconSrc = await readFile('components/icons/Icon.tsx');
  for (const m of iconSrc.matchAll(/"([a-z0-9-]+)"/g)) { /* map values validated below */ }
  const domainMap = iconSrc.match(/DOMAIN_ICON_MAP[^=]*=\s*\{([\s\S]*?)\};/)[1];
  for (const m of domainMap.matchAll(/:\s*"([a-z0-9-]+)"/g)) used.add(m[1]);
  for (const g of used) if (!defined.has(g)) errors.push('icon-map: glyph "' + g + '" referenced but not defined in icons/icons.css');

  // ---- 5. domain-state coverage (literal must render in the mandated story or component source)
  const COVERAGE = {
    'states/account-lifecycle.html': ['connecting', 'connected', 'stopped', 'disconnected', 'unknown', 'healthy', 'degraded', 'unavailable', 'expired', 'reauthenticating', 'cooling_down'],
    'states/credential-lifecycle.html': ['api_key', 'oauth2', 'github_oauth', 'copilot_service', 'active', 'staged', 'retired', 'idle', 'validating', 'swapping', 'successful', 'failed', 'rollback', 'interrupted'],
    'states/funding-evidence.html': ['free', 'paid', 'unknown', 'conflicting', 'stale', 'owner_override', 'locked', 'provider_policy', 'provider_evidence', 'owner_policy'],
    'states/capability-truth.html': ['unknown', 'supported', 'unsupported', 'pending', 'running', 'succeeded', 'inconclusive', 'retryable_failure', 'terminal_failure'],
    'states/certification.html': ['discovered', 'observed', 'probing', 'certified', 'suspended', 'expired', 'ROUTABLE'],
    'states/quota-freshness.html': ['available', 'insufficient', 'exhausted', 'unknown', 'stale', 'provider:', 'rolling:', 'local:'],
    'states/reservation-reconciliation.html': ['reserved', 'reconciliation_pending', 'settled', 'released', 'unknown_consumption'],
    'states/routing-outcomes.html': ['identity_unresolved', 'context_unverified', 'capability_not_certified', 'funding_unknown', 'no_healthy_account', 'quota_exhausted', 'quota_insufficient', 'cooling_down', 'account_stopped', 'account_disconnected', 'credential_expired', 'account_unavailable', 'reauth_in_progress', 'eligible', 'degraded', 'unroutable', 'venom_free_capacity_exhausted'],
    'states/owner-authentication.html': ['first-run', 'unauthenticated', 'active', 'idle_warning', 'expired', 'absolute_expiry', 'revoked', 'reverification_required', 'reverified', 'locked_out', 'invalid_credentials'],
    'states/async-jobs.html': ['pending', 'running', 'completed', 'failed', 'expired'],
  };
  let stateChecks = 0;
  for (const [file, needles] of Object.entries(COVERAGE)) {
    let src;
    try { src = await readFile(file); } catch (e) { errors.push('coverage: mandated story missing: ' + file); continue; }
    // The rendered JSX now lives in the sibling Vite entry module (migrated off in-browser
    // Babel) rather than inline in the .html wrapper — check both.
    const entryFile = file.replace(/\.html$/, '.entry.tsx');
    try { src += '\n' + (await readFile(entryFile)); } catch (e) { /* no entry module (e.g. static foundations page) */ }
    for (const n of needles) { stateChecks++; if (!src.includes(n)) errors.push('coverage: ' + file + ' (+ entry module) does not render state "' + n + '"'); }
  }

  // ---- 6. story coverage (generated demo bootstraps — *.entry.tsx — are not components and are exempt)
  let componentFileCount = 0;
  for (const d of COMPONENT_DIRS) {
    const cards = await filesIn(d, ['.card.html']);
    if (!cards.length) errors.push('story-coverage: ' + d + ' has no card');
    const tsx = (await filesIn(d, ['.tsx'])).filter((f) => !f.endsWith('.entry.tsx'));
    for (const t of tsx) {
      componentFileCount++;
      const base = t.replace(/\.tsx$/, '');
      try { await readFile(base + '.d.ts'); } catch (e) { errors.push('contract: missing ' + base + '.d.ts'); }
      try { await readFile(base + '.prompt.md'); } catch (e) { errors.push('contract: missing ' + base + '.prompt.md'); }
    }
  }

  // ---- 7. forbidden runtime CDN URLs — no network access anywhere in the package
  const CDN_HOSTS = ['unpkg.com', 'cdn.jsdelivr.net', 'fonts.googleapis.com', 'fonts.gstatic.com', 'cdnjs.cloudflare.com', 'jsdelivr.net'];
  let cdnFiles = 0;
  for (const d of UI_SCOPE_DIRS.concat(['tokens'])) {
    for (const f of await filesIn(d, ['.tsx', '.html', '.jsx', '.css', '.ts'])) {
      const src = await readFile(f);
      cdnFiles++;
      for (const host of CDN_HOSTS) if (src.includes(host)) errors.push('cdn: forbidden runtime dependency on "' + host + '" in ' + f);
    }
  }

  // ---- 8. required-component presence (docs/07 mandated additions)
  const REQUIRED_COMPONENTS = {
    'components/feedback/ErrorState.tsx': 'ErrorState',
    'components/actions/ThemeSwitcher.tsx': 'ThemeSwitcher',
    'components/actions/DensityToggle.tsx': 'DensityToggle',
    'components/forms/FilterBar.tsx': 'FilterBar',
  };
  for (const [file, name] of Object.entries(REQUIRED_COMPONENTS)) {
    try {
      const src = await readFile(file);
      if (!new RegExp('export function ' + name + '\\b').test(src)) errors.push('required-component: ' + file + ' does not export ' + name);
    } catch (e) {
      errors.push('required-component: missing ' + file);
    }
  }

  // ---- 9. no `any` in the public component API — .tsx (component source, the type
  // annotations are authored here) AND .d.ts (generated; catches drift/hand-editing that
  // reintroduces `any` even if the .tsx itself is clean).
  const ANY_PATTERNS = [
    { re: /\bprops:\s*any\b/g, label: 'props: any' },
    { re: /:\s*any(?=[\s,;)\]>])/g, label: ': any' },
    { re: /<any>/g, label: '<any>' },
    { re: /\bas any\b/g, label: 'as any' },
    { re: /\bunknown as\b/g, label: 'unknown as' },
    { re: /Record<string,\s*any>/g, label: 'Record<string, any>' },
  ];
  let anyScannedFiles = 0;
  for (const d of COMPONENT_DIRS) {
    const tsxFiles = (await filesIn(d, ['.tsx'])).filter((f) => !f.endsWith('.entry.tsx'));
    const dtsFiles = await filesIn(d, ['.d.ts']);
    for (const f of tsxFiles.concat(dtsFiles)) {
      const src = await readFile(f);
      anyScannedFiles++;
      for (const { re, label } of ANY_PATTERNS) {
        const m = src.match(re);
        if (m) errors.push('no-any: ' + f + ' uses "' + label + '" (' + m.length + ' occurrence' + (m.length > 1 ? 's' : '') + ') — replace with an explicit type');
      }
    }
  }

  const summary = {
    lintFiles: lintCount,
    terminologyFiles: termFiles,
    iconGlyphsDefined: defined.size,
    iconGlyphsUsed: used.size,
    stateChecks,
    componentFileCount,
    cdnScanFiles: cdnFiles,
    requiredComponentsChecked: Object.keys(REQUIRED_COMPONENTS).length,
    anyScannedFiles,
    errors,
  };
  log('GUARDRAILS: ' + lintCount + ' lint files, ' + termFiles + ' terminology files, ' + used.size + '/' + defined.size + ' glyphs, ' + stateChecks + ' state checks, ' + componentFileCount + ' components, ' + cdnFiles + ' cdn-scanned files, ' + anyScannedFiles + ' any-scanned files, ' + errors.length + ' violations');
  if (errors.length) throw new Error('GUARDRAIL FAILURES:\n' + errors.join('\n'));
  return summary;
}
if (typeof module !== 'undefined') module.exports = { checkVenomGuardrails };
