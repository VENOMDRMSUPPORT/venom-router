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
        className={`${className} rounded-xl flex items-center justify-center bg-[#0F1A14] border border-emerald-500/30`}
      >
        <svg
          viewBox="0 0 24 24"
          className="size-6"
          fill="none"
          stroke="#10b981"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden
        >
          <path d="M8 6l-5 6 5 6M16 6l5 6-5 6M14 4l-4 16" />
        </svg>
      </div>
    );
  }
  return <div className={`${className} rounded-xl bg-muted border border-border`} />;
}
