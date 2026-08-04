/**
 * The TWO account scopes every fleet surface must share, so no two counters
 * can disagree about what "the fleet" is.
 *
 * Before this existed the definition was restated inline in FleetOverview,
 * ProviderRow and AccountRow, which is how a disabled account ended up
 * counted in "1/2 account health" and reported as "1 require action" while
 * the models total already ignored it — three numbers, three answers, on one
 * screen.
 */

/** The connection-axis fields these predicates read. Deliberately structural
 * rather than the full projection so storage/test fixtures can be scoped too. */
interface ConnectionScoped {
  connection_state: string;
}

/**
 * Should this account appear in the fleet's LISTS?
 *
 * A soft-disconnected account is retained server-side for history only — it
 * is gone from the owner's view. Everything else is listed, INCLUDING a
 * stopped account: hiding that one would remove the only control that turns
 * it back on.
 */
export function isListedAccount(account: ConnectionScoped): boolean {
  return account.connection_state !== "disconnected";
}

/**
 * Should this account be COUNTED?
 *
 * No for a stopped account: the owner deliberately turned it off, so it is
 * not part of the fleet and must read as if it were not there. Counting it
 * pins "healthy" below 100% permanently and reports attention the owner has
 * already dealt with. Re-enabling it puts it straight back into every count,
 * because the counts are derived from this predicate rather than stored.
 *
 * Note this is about MEMBERSHIP, not health: a counted account may still be
 * degraded or expired — that is what the health numerator is for.
 */
export function countsTowardFleet(account: ConnectionScoped): boolean {
  return isListedAccount(account) && account.connection_state !== "stopped";
}
