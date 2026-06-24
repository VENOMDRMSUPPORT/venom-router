import { cn } from "@/lib/utils";
import { ModelCapabilityIcons } from "@/components/providers/model-capability-icons";
import { CAPABILITY_FILTER_OPTIONS, CAPABILITY_LABELS } from "./routing-constants";

export function CapabilityFilter({
  selected,
  onChange,
}: {
  selected: string[];
  onChange: (caps: string[]) => void;
}) {
  function toggle(cap: string) {
    if (selected.includes(cap)) {
      onChange(selected.filter((c) => c !== cap));
    } else {
      onChange([...selected, cap]);
    }
  }

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-1.5">
        {CAPABILITY_FILTER_OPTIONS.map((cap) => {
          const active = selected.includes(cap);
          return (
            <button
              key={cap}
              type="button"
              onClick={() => toggle(cap)}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-[11px] font-medium transition-all",
                active
                  ? "border-primary/50 bg-primary/10 text-foreground"
                  : "border-border/60 bg-background/50 text-muted-foreground hover:border-border hover:text-foreground",
              )}
            >
              <ModelCapabilityIcons capabilities={[cap]} max={1} size="sm" />
              {CAPABILITY_LABELS[cap] ?? cap}
            </button>
          );
        })}
      </div>
      {selected.length > 0 && (
        <p className="text-[10px] text-muted-foreground">
          Rules will only match requests requiring:{" "}
          {selected.map((c) => CAPABILITY_LABELS[c] ?? c).join(", ")}
        </p>
      )}
    </div>
  );
}
