/* Brand-faithful monograms for built-in integrations. */
export function ProviderIcon({
  slug,
  className = "size-11",
}: {
  slug: string;
  className?: string;
}) {
  if (slug === "claude-code") {
    // Anthropic / Claude — warm sand with stylized "C" / sunburst mark
    return (
      <div
        className={`${className} rounded-xl flex items-center justify-center bg-[#F5EFE6] dark:bg-[#1E1A14] border border-[#E8DCC5]/60 dark:border-[#3A2E1E] overflow-hidden`}
      >
        <img
          src="/providers/claude-code.svg"
          className="size-6 object-contain"
          alt="Claude Code"
          aria-hidden
        />
      </div>
    );
  }
  if (slug === "antigravity") {
    // Google Antigravity — Google "G" gradient ring
    return (
      <div
        className={`${className} rounded-xl flex items-center justify-center bg-white dark:bg-[#0F1115] border border-border overflow-hidden`}
      >
        <img
          src="/providers/antigravity.png"
          className="size-6 object-contain"
          alt="Antigravity"
          aria-hidden
        />
      </div>
    );
  }
  if (slug === "opencode-zen") {
    return (
      <div
        className={`${className} rounded-xl flex items-center justify-center bg-[#1A120E] dark:bg-[#1A120E] border border-[#E87040]/30 overflow-hidden`}
      >
        <img
          src="/providers/opencode.png"
          className="size-6 object-contain"
          alt="OpenCode Zen"
          aria-hidden
        />
      </div>
    );
  }
  return <div className={`${className} rounded-xl bg-muted border border-border`} />;
}
