import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import FirstRunSetup from "./FirstRunSetup";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("FirstRunSetup password visibility", () => {
  it("keeps both passwords masked by default and toggles each field independently", () => {
    render(<FirstRunSetup onComplete={vi.fn()} />);

    const password = screen.getByLabelText(/owner password/i) as HTMLInputElement;
    const confirmation = screen.getByLabelText(/confirm password/i) as HTMLInputElement;
    fireEvent.change(password, { target: { value: "owner-password-value" } });
    fireEvent.change(confirmation, { target: { value: "confirmation-value" } });

    expect(password.type).toBe("password");
    expect(confirmation.type).toBe("password");

    const passwordToggle = screen.getByRole("button", { name: "Show password" });
    expect(passwordToggle.getAttribute("aria-pressed")).toBe("false");
    expect(passwordToggle.querySelector(".vn-icon--eye")).not.toBeNull();
    fireEvent.click(passwordToggle);

    expect(password.type).toBe("text");
    expect(password.value).toBe("owner-password-value");
    expect(confirmation.type).toBe("password");
    const hidePassword = screen.getByRole("button", { name: "Hide password" });
    expect(hidePassword.getAttribute("aria-pressed")).toBe("true");
    expect(hidePassword.querySelector(".vn-icon--eye-off")).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Show password confirmation" }));
    expect(password.type).toBe("text");
    expect(confirmation.type).toBe("text");
    expect(confirmation.value).toBe("confirmation-value");

    fireEvent.click(screen.getByRole("button", { name: "Hide password" }));
    expect(password.type).toBe("password");
    expect(confirmation.type).toBe("text");
  });

  it("disables both visibility controls while setup is submitting", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    render(<FirstRunSetup onComplete={vi.fn()} />);

    fireEvent.change(screen.getByLabelText(/owner password/i), {
      target: { value: "a-valid-owner-password" },
    });
    fireEvent.change(screen.getByLabelText(/confirm password/i), {
      target: { value: "a-valid-owner-password" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create password/i }));

    await waitFor(() => {
      expect(
        (screen.getByRole("button", { name: "Show password" }) as HTMLButtonElement).disabled,
      ).toBe(true);
      expect(
        (screen.getByRole("button", { name: "Show password confirmation" }) as HTMLButtonElement)
          .disabled,
      ).toBe(true);
    });
  });
});
