import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Link2 } from "lucide-react";
import { ProviderIcon } from "./provider-icon";

export function IntegrationCard({
  slug,
  name,
  homepage,
  description,
  authType,
  connectLabel = "Connect Integration",
  onConnect,
  footer,
}: {
  slug: string;
  name: string;
  homepage?: string | null;
  description?: string | null;
  authType: string;
  connectLabel?: string;
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
    <Card className="p-5 flex flex-col gap-4 border border-border bg-card hover:border-primary/50 hover:shadow-md transition-all">
      <div className="flex items-start justify-between gap-3">
        <ProviderIcon slug={slug} />
        <Badge
          variant="outline"
          className="text-[10px] font-mono tracking-wider border-border bg-muted/40 text-muted-foreground"
        >
          {badge}
        </Badge>
      </div>
      <div className="space-y-1">
        <h3 className="font-semibold text-base leading-tight">{name}</h3>
        {homepage && <p className="text-xs text-muted-foreground truncate">{homepage}</p>}
      </div>
      {description && (
        <p className="text-sm text-muted-foreground leading-relaxed flex-1 line-clamp-3">
          {description}
        </p>
      )}
      {footer}
      <Button
        variant="outline"
        className="w-full justify-center gap-2 border-border hover:border-primary/50 hover:bg-primary/5"
        onClick={onConnect}
      >
        <Link2 className="size-4" />
        {connectLabel}
      </Button>
    </Card>
  );
}
