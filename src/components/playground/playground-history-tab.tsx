import { useSuspenseQuery, queryOptions } from "@tanstack/react-query";
import { History, CheckCircle2, AlertCircle } from "lucide-react";
import { Card } from "@/components/ui/card";
import { api } from "@/lib/api-client";
import { formatRelativeTime } from "@/lib/utils";
import type { UsageAnalytics } from "@/lib/db/usage.server";

export function PlaygroundHistoryTab() {
  const { data } = useSuspenseQuery(
    queryOptions({
      queryKey: ["usage-analytics", "7d"],
      queryFn: () => api.get<UsageAnalytics>("/api/dashboard/usage?period=7d"),
    }),
  );

  const recent = data.recent.slice(0, 20);

  if (recent.length === 0) {
    return (
      <Card className="border-border/60 p-12 text-center space-y-3">
        <History className="size-10 mx-auto text-muted-foreground" />
        <p className="font-medium">No history yet</p>
        <p className="text-sm text-muted-foreground">
          Send your first request in the Playground tab.
        </p>
      </Card>
    );
  }

  return (
    <Card className="border-border/60 overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/30 text-left text-[11px] uppercase tracking-wider text-muted-foreground">
              <th className="px-4 py-3 font-medium">Model</th>
              <th className="px-4 py-3 font-medium">Status</th>
              <th className="px-4 py-3 font-medium">Tokens</th>
              <th className="px-4 py-3 font-medium">Cost</th>
              <th className="px-4 py-3 font-medium">Time</th>
            </tr>
          </thead>
          <tbody>
            {recent.map((r) => (
              <tr key={r.id} className="border-b border-border/50 hover:bg-muted/20">
                <td className="px-4 py-2.5">
                  <code className="text-xs font-mono text-primary">venom/{r.venom_slug}</code>
                </td>
                <td className="px-4 py-2.5">
                  {r.success !== false ? (
                    <span className="inline-flex items-center gap-1 text-emerald-500 text-xs">
                      <CheckCircle2 className="h-3.5 w-3.5" /> ok
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 text-red-500 text-xs">
                      <AlertCircle className="h-3.5 w-3.5" /> failed
                    </span>
                  )}
                </td>
                <td className="px-4 py-2.5 tabular-nums text-xs text-muted-foreground">
                  {((r.input_tokens ?? 0) + (r.output_tokens ?? 0)).toLocaleString()}
                </td>
                <td className="px-4 py-2.5 tabular-nums text-xs text-muted-foreground">
                  {r.cost_usd != null ? `$${Number(r.cost_usd).toFixed(5)}` : "—"}
                </td>
                <td className="px-4 py-2.5 text-xs text-muted-foreground">
                  {formatRelativeTime(r.created_at)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
