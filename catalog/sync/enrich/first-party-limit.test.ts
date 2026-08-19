/**
 * The adoption rule for a vendor's own figure.
 *
 * Reached only after the host has been asked and published nothing. Measured
 * 2026-08-18: ClinePass serves 13 models and its roster endpoint returns
 * `{id, name, description, tags}` for every one of them; its second endpoint
 * carries no `cline-pass/` id at all. So for `cline-pass/glm-5.3` there is no
 * seller figure to prefer — the choice is the vendor's number or nothing.
 */

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { adoptFirstPartyLimit } from './resolvers.ts';

describe('adopting a limit the model vendor published about its own model', () => {
  test('one storefront is enough', () => {
    assert.deepEqual(
      adoptFirstPartyLimit([{ value: 1_000_000, by: 'zai-coding-plan/glm-5.3', url: 'https://docs.z.ai/devpack/overview' }]),
      { value: 1_000_000, by: 'zai-coding-plan/glm-5.3', url: 'https://docs.z.ai/devpack/overview' },
    );
  });

  test('storefronts that agree are adopted, and the first one is credited', () => {
    // Z-AI's real case: both of its own plans publish 1,000,000 for glm-5.3.
    assert.deepEqual(
      adoptFirstPartyLimit([
        { value: 1_000_000, by: 'zai-coding-plan/glm-5.3', url: 'https://docs.z.ai/devpack/overview' },
        { value: 1_000_000, by: 'zhipuai-coding-plan/glm-5.3', url: 'https://docs.bigmodel.cn/' },
      ]),
      { value: 1_000_000, by: 'zai-coding-plan/glm-5.3', url: 'https://docs.z.ai/devpack/overview' },
    );
  });

  test('storefronts that disagree adopt nothing', () => {
    // Unknown stays ineligible. A vendor contradicting itself is not a weaker
    // version of a published fact — it is the absence of one, and picking the
    // larger, the smaller or the more popular figure would each publish a
    // number no source stated for this host.
    assert.equal(
      adoptFirstPartyLimit([
        { value: 1_000_000, by: 'zai-coding-plan/glm-5.3', url: 'https://docs.z.ai/devpack/overview' },
        { value: 200_000, by: 'zhipuai-coding-plan/glm-5.3', url: 'https://docs.bigmodel.cn/' },
      ]),
      null,
    );
  });

  test('a majority does not carry a disagreement', () => {
    // Storefront count is not evidence: a vendor with four shopfronts is not
    // more credible than one with a single shopfront.
    assert.equal(
      adoptFirstPartyLimit([
        { value: 1_000_000, by: 'a/m', url: null },
        { value: 1_000_000, by: 'b/m', url: null },
        { value: 200_000, by: 'c/m', url: null },
      ]),
      null,
    );
  });

  test('nothing declared adopts nothing', () => {
    assert.equal(adoptFirstPartyLimit([]), null);
  });
});
