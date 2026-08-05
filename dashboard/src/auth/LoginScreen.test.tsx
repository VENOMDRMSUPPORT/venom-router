import { describe, expect, it } from "vitest";
import { autofillValueFromAnimation } from "./autofill";

// jsdom has no AnimationEvent implementation (window.AnimationEvent is
// undefined), so React never wires a listener for the real "animationstart"
// DOM event under jsdom — there is no way to drive the actual browser
// autofill trigger through a unit test. What's actually worth pinning down
// is the decision it makes once fired, so that logic is a pure function
// tested directly here; the one-line onAnimationStart wiring in LoginScreen
// is exercised only by a real browser.
describe("autofillValueFromAnimation", () => {
  it("returns the DOM value when the autofill-detection animation fires", () => {
    expect(autofillValueFromAnimation("vn-autofill-start", "autofilled-owner-password")).toBe(
      "autofilled-owner-password",
    );
  });

  it("ignores unrelated animations", () => {
    expect(autofillValueFromAnimation("some-other-animation", "autofilled-owner-password")).toBeNull();
  });
});
