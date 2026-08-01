import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import ProviderLogo from "./ProviderLogo";
import { PROVIDER_LOGO_SLUGS, providerLogoSrc } from "./providerLogos";

afterEach(cleanup);

describe("ProviderLogo", () => {
  it("renders the official logo img with the static src and the display name as alt for a known slug", () => {
    render(<ProviderLogo slug="opencode-zen" name="OpenCode Zen" size="lg" />);

    const img = screen.getByRole("img", { name: "OpenCode Zen" });
    expect(img.tagName).toBe("IMG");
    expect(img.getAttribute("src")).toBe("/providers/opencode-zen.png");
    expect(img.getAttribute("alt")).toBe("OpenCode Zen");
    // The logo sits in the DS mark frame — same square dimensions and
    // token-rounded border as the letter avatar it replaces.
    expect(img.parentElement?.className).toBe("vn-mark vn-mark--lg");
  });

  it("falls back to the DS letter mark for a slug with no shipped logo", () => {
    render(<ProviderLogo slug="agnes-ai" name="Agnes AI" />);

    const mark = screen.getByRole("img", { name: "Agnes AI" });
    expect(mark.tagName).toBe("SPAN");
    expect(mark.textContent).toBe("AA");
    expect(mark.querySelector("img")).toBeNull();
  });

  it("falls back to the letter mark when the logo image fails to load — never a broken image", () => {
    render(<ProviderLogo slug="xai" name="xAI (Grok)" />);

    fireEvent.error(screen.getByRole("img", { name: "xAI (Grok)" }));

    const mark = screen.getByRole("img", { name: "xAI (Grok)" });
    expect(mark.tagName).toBe("SPAN");
    expect(mark.querySelector("img")).toBeNull();
  });

  it("ships exactly one public/providers/<slug>.png per manifest slug — no drift in either direction", () => {
    // import.meta.glob enumerates the real files on disk at transform
    // time (no node:fs — this project deliberately has no @types/node).
    const shipped = Object.keys(import.meta.glob("../../public/providers/*"))
      .map((p) => p.split("/").pop() as string)
      .sort();
    const expected = [...PROVIDER_LOGO_SLUGS].map((slug) => `${slug}.png`).sort();
    expect(shipped).toEqual(expected);
  });

  it("providerLogoSrc is null for unknown slugs and the custom path", () => {
    expect(providerLogoSrc("custom")).toBeNull();
    expect(providerLogoSrc("agnes-ai")).toBeNull();
    expect(providerLogoSrc("not-a-provider")).toBeNull();
  });
});
