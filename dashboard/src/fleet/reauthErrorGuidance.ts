/**
 * Owner-facing guidance for the fixed vocabulary POST /oauth/complete and the
 * reauth status poll can return (httpapi's `safeOAuthErrorCode`).
 *
 * WHY: those codes are deliberately machine-safe — the backend refuses to echo
 * adapter text it cannot vouch for — but that means the raw code is ALL the
 * dashboard receives, and rendering it verbatim tells the owner nothing. The
 * live case that forced this: signing in as a second ClinePass account showed
 * `account_identity_mismatch` + "the OAuth code could not be completed" +
 * a "not retryable" badge. Every part of that was unhelpful or wrong — the
 * cause (wrong account) was invisible, the fix (use this account) was
 * unstated, and the action retries perfectly well with the right account.
 *
 * `retryable` here answers the owner's question — "is it worth trying again?"
 * — not the transport's. A mismatch IS retryable; an expired window IS
 * retryable (start over). Only a genuinely stuck state says otherwise.
 */

export interface ReauthGuidance {
  message: string;
  retryable: boolean;
}

/**
 * Rewrites one completion error into a cause + fix.
 *
 * accountLabel is what the owner should sign in AS — the account's identity
 * email when known, so the message names the very thing that went wrong.
 * Unknown codes fall through to the server's own message: inventing guidance
 * for a code this map has never seen would be worse than passing it along.
 */
export function reauthErrorGuidance(
  code: string,
  serverMessage: string,
  accountLabel: string,
): ReauthGuidance {
  switch (code) {
    case "account_identity_mismatch":
      return {
        // The guard is correct and must stay: swapping one account's
        // credential into another would attribute a subscribed session to an
        // unsubscribed account. So the message explains, never apologises.
        message: `You signed in as a different account. This row is ${accountLabel} — sign in as that account to restore it.`,
        retryable: true,
      };
    case "invalid_or_expired":
      return {
        message: "The sign-in window expired before it was completed. Start sign-in again.",
        retryable: true,
      };
    case "reauthentication_in_progress":
      return {
        message: "A sign-in for this account is already running. Finish or close it, then retry.",
        retryable: false,
      };
    case "invalid_credential":
      return {
        message: "The provider rejected this sign-in. Sign in again to restore this account.",
        retryable: true,
      };
    case "provider_unavailable":
      return {
        message: "The provider could not be reached. This is temporary — try again shortly.",
        retryable: true,
      };
    case "oauth_denied":
      return {
        message: "The sign-in was denied at the provider. Approve access to restore this account.",
        retryable: true,
      };
    default:
      // An unmapped code keeps the server's message AND the code itself: this
      // map covers the reauth vocabulary, but the same slot renders every
      // other account action (quota, discovery, stop/resume), where the typed
      // code is the diagnosable part and dropping it would silently weaken
      // "a real failure always surfaces". Inventing guidance for a code this
      // map has never seen would be worse than passing it through.
      return { message: code ? `${serverMessage} (${code})` : serverMessage, retryable: true };
  }
}
