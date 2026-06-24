import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CheckCircle2, Link2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { ProviderIcon } from "./provider-icon";

export function IntegrationCard({
  slug,
  name,
  homepage,
  description,
  authType,
  connectLabel = "Connect Integration",
  connected = false,
  accountCount = 0,
  onConnect,
  footer,
}: {
  slug: string;
  name: string;
  homepage?: string | null;
  description?: string | null;
  authType: string;
  connectLabel?: string;
  connected?: boolean;
  accountCount?: number;
  onConnect?: () => void;
  footer?: ReactNode;
}) {
  const badge =
    authType === "oauth2_pkce"
      ? "OAUTH 2 · PKCE"
      : authType === "oauth2_secret"
        ? "OAUTH 2"
        : "API KEY";
  return (
    <Card
      className={cn(
        "p-5 flex flex-col gap-4 border transition-all",
        connected
          ? "border-emerald-500/25 bg-emerald-500/[0.03] opacity-90"
          : "border-border bg-card hover:border-primary/50 hover:shadow-md",
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <ProviderIcon slug={slug} />
        <div className="flex flex-col items-end gap-1.5">
          {connected && (
            <Badge className="text-[10px] font-semibold tracking-wider bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border-emerald-500/25 hover:bg-emerald-500/15">
              <CheckCircle2 className="size-3 mr-1" />
              CONNECTED
            </Badge>
          )}
          <Badge
            variant="outline"
            className="text-[10px] font-mono tracking-wider border-border bg-muted/40 text-muted-foreground"
          >
            {badge}
          </Badge>
        </div>
      </div>
      <div className="space-y-1">
        <h3 className="font-semibold text-base leading-tight">{name}</h3>
        {homepage && <p className="text-xs text-muted-foreground truncate">{homepage}</p>}
        {connected && accountCount > 0 && (
          <p className="text-xs text-emerald-600 dark:text-emerald-400">
            {accountCount} account{accountCount === 1 ? "" : "s"} linked
          </p>
        )}
      </div>
      {description && (
        <p className="text-sm text-muted-foreground leading-relaxed flex-1 line-clamp-3">
          {description}
        </p>
      )}
      {footer}
      <Button
        variant="outline"
        className={cn(
          "w-full justify-center gap-2",
          connected
            ? "border-emerald-500/20 text-muted-foreground cursor-not-allowed"
            : "border-border hover:border-primary/50 hover:bg-primary/5",
        )}
        onClick={connected ? undefined : onConnect}
        disabled={connected}
      >
        {connected ? (
          <>
            <CheckCircle2 className="size-4 text-emerald-500" />
            Already connected
          </>
        ) : (
          <>
            <Link2 className="size-4" />
            {connectLabel}
          </>
        )}
      </Button>
    </Card>
  );
}
