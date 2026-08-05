/** Chrome/Edge autofill writes the password field's DOM value directly,
 * bypassing the onChange event React relies on for controlled state — so a
 * password-manager fill leaves `password` state stuck at "" and Sign in
 * stuck disabled despite the field visibly holding the filled value.
 * `vn-autofill-start` (components-core.css) is a no-op animation that only
 * plays while the input matches `:-webkit-autofill`, giving `onAnimationStart`
 * a real signal to pull that value back into state — the standard browser
 * hook for this. Any other animation name is unrelated and ignored.
 *
 * Lives in its own file (not LoginScreen.tsx) so the component file only
 * exports components — the react-refresh zero-warning baseline. */
export function autofillValueFromAnimation(animationName: string, domValue: string): string | null {
  return animationName === "vn-autofill-start" ? domValue : null;
}
