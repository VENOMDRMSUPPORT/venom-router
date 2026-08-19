/**
 * Load the model-vendor registry.
 *
 * One module rather than a copy in each entry point. The CLI and the service
 * both reach the same pipeline, and a source configured in only one of them is
 * the exact shape of the bug `sync/pipeline.ts` exists to prevent: the service
 * would run every six hours with no first-party evidence available and quietly
 * undo what a CLI run had filled.
 */

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { VendorRegistry } from './sources/models-dev.ts';

const HERE = dirname(fileURLToPath(import.meta.url));

export function loadVendors(): VendorRegistry {
  return JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'vendors.json'), 'utf8')).vendors ?? {};
}
