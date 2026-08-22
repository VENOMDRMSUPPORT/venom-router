import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { ADAPTERS } from './index.ts';

/**
 * An adapter's `officialFreeList` is the PROOF that a free-only provider serves
 * an id for nothing — it outranks the third-party price feed, so it is the most
 * load-bearing hand-reviewed claim in the sync.
 *
 * It ships as a TypeScript literal rather than a validated overlay file, which
 * means nothing validated it. `reviewedAt` and `sourceUrl` are carried "so the
 * list is auditable and re-checkable", and were read by nothing at all: a typo
 * in the URL, a duplicated id, or a review date from the future would all have
 * shipped silently. These are the checks the overlay parsers already apply to
 * display names and evaluation identities, applied to the one reviewed claim
 * that lives in code.
 *
 * Deliberately NOT a staleness deadline. A test that starts failing on a date
 * nobody chose fails in CI for a reason unrelated to the change in front of it;
 * re-reading the source is a review task, not a build gate.
 */
describe('every declared official free list carries provenance that holds up', () => {
  const declared = ADAPTERS.filter((adapter) => adapter.officialFreeList);

  test('at least one provider proves freeness from the provider itself', () => {
    assert.ok(declared.length > 0, 'nothing exercises the officialFreeList path');
  });

  for (const adapter of declared) {
    const list = adapter.officialFreeList!;

    describe(adapter.id, () => {
      test('the list is only consulted where the policy reads it', () => {
        // `applyPublishPolicy` asks for it on a `free_only` provider and nowhere
        // else, so a list on any other provider is configuration that does
        // nothing while looking like it governs publication.
        assert.equal(adapter.publishPolicy, 'free_only');
      });

      test('it names ids, each one exactly once', () => {
        assert.ok(list.ids.length > 0, 'an empty list withholds the whole roster');
        for (const id of list.ids) {
          assert.equal(typeof id, 'string');
          assert.equal(id, id.trim());
          assert.ok(id.length > 0);
        }
        assert.equal(new Set(list.ids).size, list.ids.length, 'a duplicated id is a review that was applied twice');
      });

      test('the source is an HTTPS document a reviewer can open', () => {
        assert.match(list.sourceUrl, /^https:\/\/\S+$/);
        assert.doesNotThrow(() => new URL(list.sourceUrl));
      });

      test('the review date is a real calendar day, and not in the future', () => {
        assert.match(list.reviewedAt, /^\d{4}-\d{2}-\d{2}$/);
        const reviewed = new Date(`${list.reviewedAt}T00:00:00.000Z`);
        assert.ok(!Number.isNaN(reviewed.getTime()), 'not a date');
        assert.equal(reviewed.toISOString().slice(0, 10), list.reviewedAt, 'not a real calendar day');
        assert.ok(reviewed.getTime() <= Date.now(), 'a review cannot have happened in the future');
      });
    });
  }
});
