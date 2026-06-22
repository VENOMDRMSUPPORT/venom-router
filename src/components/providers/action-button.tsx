import { AlertCircle, CheckCircle2, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";

export function ActionButton({
  icon: Icon,
  onClick,
  status = "idle",
  title,
  disabled,
  activeColor = "text-muted-foreground",
}: {
  icon: React.ComponentType<{ className?: string }>;
  onClick: () => void;
  status?: "idle" | "loading" | "success" | "error";
  title: string;
  disabled?: boolean;
  activeColor?: string;
}) {
  const stateClasses =
    status === "loading"
      ? "text-amber-500 bg-amber-500/10"
      : status === "success"
        ? "text-emerald-500 bg-emerald-500/10"
        : status === "error"
          ? "text-red-500 bg-red-500/10"
          : cn(activeColor, "hover:bg-accent hover:text-foreground");

  const DisplayIcon =
    status === "loading"
      ? Loader2
      : status === "success"
        ? CheckCircle2
        : status === "error"
          ? AlertCircle
          : Icon;

  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={cn(
        "flex h-7 w-7 items-center justify-center rounded-md transition-all duration-200 disabled:opacity-40 disabled:cursor-not-allowed",
        stateClasses,
      )}
    >
      <DisplayIcon className={cn("h-3.5 w-3.5", status === "loading" && "animate-spin")} />
    </button>
  );
}
