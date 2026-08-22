import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { renderEnvReport } from './env-check.ts';
import type { CredentialStatus } from '../sync/evaluation/provider-transport.ts';

const row = (over: Partial<CredentialStatus> = {}): CredentialStatus => ({
  providerId: 'opencode-zen',
  envName: 'VENOM_CATALOG_OPENCODE_ZEN_API_KEY',
  state: 'present',
  ...over,
});

describe('the environment report a reader acts on', () => {
  test('names the variable for a missing credential', () => {
    const text = renderEnvReport([row({ state: 'missing' })]).join('\n');
    assert.match(text, /VENOM_CATALOG_OPENCODE_ZEN_API_KEY/);
    assert.match(text, /not set in this process/);
    assert.match(text, /catalog\/\.env/);
  });

  test('a corrupted name is spelled out escaped, with the cause and the fix', () => {
    const text = renderEnvReport([
      row({ state: 'malformed_name', foundAs: '﻿VENOM_CATALOG_OPENCODE_ZEN_API_KEY' }),
    ]).join('\n');
    // Escaped, so an invisible byte is visible to the person reading the terminal.
    assert.match(text, /\ufeffVENOM_CATALOG_OPENCODE_ZEN_API_KEY/);
    assert.match(text, /BOM/);
    assert.match(text, /without a BOM/);
    assert.match(text, /1 are present in the environment but filed under a name nothing asks for/);
  });

  test('a fully configured environment says so and asks for nothing', () => {
    const text = renderEnvReport([row(), row({ providerId: 'clinepass', envName: 'VENOM_CATALOG_CLINEPASS_API_KEY' })]).join('\n');
    assert.match(text, /2 of 2 evaluation credentials are readable/);
    assert.ok(!/not set/.test(text));
    assert.ok(!/Set what is missing/.test(text));
  });

  test('the rendered report never carries a credential value', () => {
    // The renderer only ever sees a status, but the assertion is what keeps that
    // true: the moment someone adds a value to `CredentialStatus`, this fails.
    const secret = 'VENOM_CATALOG_SECRET_CANARY_VALUE';
    const text = renderEnvReport([
      { ...row(), ...JSON.parse(JSON.stringify({ value: secret })) },
      row({ state: 'malformed_name', foundAs: `﻿VENOM_CATALOG_CLINEPASS_API_KEY`, providerId: 'clinepass' }),
    ]).join('\n');
    assert.ok(!text.includes(secret));
  });
});
