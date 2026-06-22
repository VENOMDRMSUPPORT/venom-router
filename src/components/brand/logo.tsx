import { cn } from "@/lib/utils";

interface LogoMarkProps {
  className?: string;
  size?: number;
}

export function LogoMark({ className, size = 32 }: LogoMarkProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 48 48"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={cn(className)}
      aria-hidden="true"
    >
      <defs>
        <linearGradient id="vr-grad" x1="0" y1="0" x2="48" y2="48" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="oklch(0.68 0.21 275)" />
          <stop offset="100%" stopColor="oklch(0.55 0.22 275)" />
        </linearGradient>
        <linearGradient id="vr-bolt" x1="14" y1="6" x2="34" y2="42" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#fff" stopOpacity="0.98" />
          <stop offset="100%" stopColor="#fff" stopOpacity="0.85" />
        </linearGradient>
      </defs>
      <rect x="2" y="2" width="44" height="44" rx="11" fill="url(#vr-grad)" />
      <path d="M2 2 H 46 A0 0 0 0 1 46 2 V 24 C 36 14, 12 14, 2 24 Z" fill="#fff" opacity="0.08" />
      <path d="M16 9 L26 9 L20 22 L29 22 L18 39 L23 26 L14 26 Z" fill="url(#vr-bolt)" />
    </svg>
  );
}

interface LogoProps {
  className?: string;
  showWordmark?: boolean;
  subtitle?: string;
  size?: number;
}

export function Logo({
  className,
  showWordmark = true,
  subtitle = "AI CONTROL CENTER",
  size = 32,
}: LogoProps) {
  return (
    <div className={cn("flex items-center gap-2.5", className)}>
      <LogoMark size={size} />
      {showWordmark && (
        <div className="flex flex-col leading-none min-w-0">
          <span className="font-display text-[15px] font-bold tracking-tight truncate text-sidebar-foreground">
            Venom<span className="font-light opacity-60">Router</span>
          </span>
          {subtitle && (
            <span className="text-[9px] font-medium tracking-[0.18em] text-sidebar-muted mt-1 truncate">
              {subtitle}
            </span>
          )}
        </div>
      )}
    </div>
  );
}
