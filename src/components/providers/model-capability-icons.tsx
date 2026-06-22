import type { LucideIcon } from "lucide-react";
import {
  MessageSquare,
  Wrench,
  Eye,
  Brain,
  Sparkles,
  Zap,
  Code2,
  Image,
  MoreHorizontal,
} from "lucide-react";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

const CAPABILITY_META: Record<string, { icon: LucideIcon; label: string; tint: string }> = {
  chat: {
    icon: MessageSquare,
    label: "Chat",
    tint: "text-sky-600 dark:text-sky-400 bg-sky-500/10 border-sky-500/20",
  },
  tools: {
    icon: Wrench,
    label: "Tools",
    tint: "text-violet-600 dark:text-violet-400 bg-violet-500/10 border-violet-500/20",
  },
  vision: {
    icon: Eye,
    label: "Vision",
    tint: "text-emerald-600 dark:text-emerald-400 bg-emerald-500/10 border-emerald-500/20",
  },
  reasoning: {
    icon: Brain,
    label: "Reasoning",
    tint: "text-amber-600 dark:text-amber-400 bg-amber-500/10 border-amber-500/20",
  },
  thinking: {
    icon: Sparkles,
    label: "Thinking",
    tint: "text-fuchsia-600 dark:text-fuchsia-400 bg-fuchsia-500/10 border-fuchsia-500/20",
  },
  streaming: {
    icon: Zap,
    label: "Streaming",
    tint: "text-cyan-600 dark:text-cyan-400 bg-cyan-500/10 border-cyan-500/20",
  },
  code: {
    icon: Code2,
    label: "Code",
    tint: "text-indigo-600 dark:text-indigo-400 bg-indigo-500/10 border-indigo-500/20",
  },
  images: {
    icon: Image,
    label: "Images",
    tint: "text-emerald-600 dark:text-emerald-400 bg-emerald-500/10 border-emerald-500/20",
  },
};

function metaFor(cap: string) {
  const key = cap.toLowerCase();
  return (
    CAPABILITY_META[key] ?? {
      icon: MoreHorizontal,
      label: cap,
      tint: "text-muted-foreground bg-muted/60 border-border",
    }
  );
}

export function ModelCapabilityIcons({
  capabilities,
  max = 6,
  size = "sm",
}: {
  capabilities: string[];
  max?: number;
  size?: "sm" | "md";
}) {
  const shown = capabilities.slice(0, max);
  const extra = capabilities.length - shown.length;
  const iconSize = size === "md" ? "h-3.5 w-3.5" : "h-3 w-3";
  const boxSize = size === "md" ? "h-7 w-7" : "h-6 w-6";

  return (
    <TooltipProvider delayDuration={200}>
      <div className="flex flex-wrap items-center gap-1">
        {shown.map((cap) => {
          const { icon: Icon, label, tint } = metaFor(cap);
          return (
            <Tooltip key={cap}>
              <TooltipTrigger asChild>
                <span
                  className={cn(
                    "inline-flex items-center justify-center rounded-md border shrink-0",
                    boxSize,
                    tint,
                  )}
                  aria-label={label}
                >
                  <Icon className={iconSize} strokeWidth={2} />
                </span>
              </TooltipTrigger>
              <TooltipContent side="top">{label}</TooltipContent>
            </Tooltip>
          );
        })}
        {extra > 0 && (
          <Tooltip>
            <TooltipTrigger asChild>
              <span
                className={cn(
                  "inline-flex items-center justify-center rounded-md border border-border bg-muted/50 text-muted-foreground text-[10px] font-medium shrink-0",
                  boxSize,
                )}
              >
                +{extra}
              </span>
            </TooltipTrigger>
            <TooltipContent side="top">{capabilities.slice(max).join(", ")}</TooltipContent>
          </Tooltip>
        )}
      </div>
    </TooltipProvider>
  );
}
