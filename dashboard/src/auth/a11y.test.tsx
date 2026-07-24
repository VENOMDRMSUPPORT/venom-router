import { afterEach, describe, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { assertNoAxeViolations } from "../test/axe";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import FirstRunSetup from "./FirstRunSetup";
import LoginScreen from "./LoginScreen";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("accessibility — owner-auth states", () => {
  it("First-run setup has zero axe violations", async () => {
    const { container } = render(<FirstRunSetup onComplete={() => {}} />);
    await assertNoAxeViolations(container);
  });

  it("Login has zero axe violations", async () => {
    const { container } = render(<LoginScreen onSuccess={() => {}} />);
    await assertNoAxeViolations(container);
  });

  it("the locked-out Login state has zero axe violations", async () => {
    const fetchMock = createFetchMock({
      "POST /api/control/v1/auth/login": () =>
        jsonResponse(429, {
          error: {
            code: "locked_out",
            message: "too many failed attempts, try again later",
            request_id: "r1",
            retryable: true,
            retry_after: 30,
          },
        }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const { container } = render(<LoginScreen onSuccess={() => {}} />);
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: "wrong-password" } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    await screen.findByText(/locked_out/i);
    await assertNoAxeViolations(container);
  });

  it("the invalid-credentials Login error state has zero axe violations", async () => {
    const fetchMock = createFetchMock({
      "POST /api/control/v1/auth/login": () =>
        jsonResponse(401, { error: { code: "invalid_credentials", message: "invalid credentials", request_id: "r2", retryable: false } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const { container } = render(<LoginScreen onSuccess={() => {}} />);
    fireEvent.change(screen.getByLabelText(/owner password/i), { target: { value: "wrong-password" } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    await screen.findByText(/invalid_credentials/i);
    await assertNoAxeViolations(container);
  });
});
