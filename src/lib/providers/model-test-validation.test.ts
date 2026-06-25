import { describe, expect, it } from "vitest";
import {
  responseContainsOk,
  validateModelTestResponse,
} from "./adapters/_shared/model-test-validation.server";

describe("model-test-validation", () => {
  it("accepts ok in response text", () => {
    expect(responseContainsOk("ok")).toBe(true);
    expect(responseContainsOk("OK")).toBe(true);
    expect(validateModelTestResponse("Sure, ok.").ok).toBe(true);
  });

  it("rejects empty or missing ok", () => {
    expect(validateModelTestResponse("").ok).toBe(false);
    expect(validateModelTestResponse("hello").ok).toBe(false);
  });
});
