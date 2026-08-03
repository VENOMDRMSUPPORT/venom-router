import { describe, expect, it } from "vitest";
import { CONNECT_CLIENT_KEY, parseLocation, pathForRoute } from "./route";

describe("pathForRoute", () => {
  it("maps the default page (overview) to the root path", () => {
    expect(pathForRoute("overview")).toBe("/");
  });

  it("maps a normal page to a single path segment matching its nav key", () => {
    expect(pathForRoute("providers")).toBe("/providers");
    expect(pathForRoute("models")).toBe("/models");
    expect(pathForRoute("token-health")).toBe("/token-health");
    expect(pathForRoute("api-keys")).toBe("/api-keys");
  });

  it("carries a diagnostics deep-link request id as a sub-path", () => {
    expect(pathForRoute("diagnostics", "req-123")).toBe("/diagnostics/routes/req-123");
  });

  it("percent-encodes an awkward request id", () => {
    expect(pathForRoute("diagnostics", "a/b c")).toBe("/diagnostics/routes/a%2Fb%20c");
  });

  it("maps diagnostics with no request id to the bare page", () => {
    expect(pathForRoute("diagnostics")).toBe("/diagnostics");
  });

  it("maps the internal connect-client pseudo-page to a real path", () => {
    expect(pathForRoute(CONNECT_CLIENT_KEY)).toBe("/connect-client");
  });
});

describe("parseLocation", () => {
  it("resolves the root path to the default page", () => {
    expect(parseLocation("/")).toEqual({ navKey: "overview" });
    expect(parseLocation("")).toEqual({ navKey: "overview" });
  });

  it("resolves a known single segment to its page", () => {
    expect(parseLocation("/providers")).toEqual({ navKey: "providers" });
    expect(parseLocation("/token-health")).toEqual({ navKey: "token-health" });
  });

  it("resolves the diagnostics deep link with its request id", () => {
    expect(parseLocation("/diagnostics/routes/req-123")).toEqual({
      navKey: "diagnostics",
      requestID: "req-123",
    });
  });

  it("decodes a percent-encoded request id", () => {
    expect(parseLocation("/diagnostics/routes/a%2Fb%20c")).toEqual({
      navKey: "diagnostics",
      requestID: "a/b c",
    });
  });

  it("resolves the connect-client path back to the pseudo-key", () => {
    expect(parseLocation("/connect-client")).toEqual({ navKey: CONNECT_CLIENT_KEY });
  });

  it("falls back to the default page for an unknown path", () => {
    expect(parseLocation("/nope")).toEqual({ navKey: "overview" });
    expect(parseLocation("/providers/extra/junk")).toEqual({ navKey: "overview" });
  });

  it("round-trips every mapping (path -> route -> path)", () => {
    for (const [navKey, requestID] of [
      ["overview", undefined],
      ["providers", undefined],
      ["diagnostics", "r1"],
      [CONNECT_CLIENT_KEY, undefined],
    ] as const) {
      const path = pathForRoute(navKey, requestID);
      expect(parseLocation(path)).toEqual(requestID ? { navKey, requestID } : { navKey });
    }
  });
});
