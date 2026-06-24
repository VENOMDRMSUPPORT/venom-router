import { CheckCircle2, RefreshCw } from "lucide-react";
import { Card } from "@/components/ui/card";
import type { PlaygroundResponse } from "./playground-types";

type Props = {
  result: PlaygroundResponse | null;
};

export function PlaygroundResponsePanel({ result }: Props) {
  if (!result) {
    return (
      <Card className="border-border/60 border-dashed p-8 text-center">
        <p className="text-sm text-muted-foreground">Response will appear here after you send.</p>
      </Card>
    );
  }

  return (
    <Card className="border-border/60 overflow-hidden">
      <div className="flex items-center justify-between flex-wrap gap-2 border-b border-border/50 px-4 py-2.5 bg-muted/20">
        <span className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
          Response
        </span>
        <div className="flex items-center gap-3 text-[10px] text-muted-foreground flex-wrap">
          <span className="flex items-center gap-1">
            <CheckCircle2 className="h-3 w-3 text-emerald-500" />
            {result.latency_ms}ms
          </span>
          <span>{(result.input_tokens + result.output_tokens).toLocaleString()} tokens</span>
          <span>${result.cost_usd.toFixed(5)}</span>
          <span className="capitalize">{result.modality}</span>
          {result.fallback_used && (
            <span className="text-amber-500 flex items-center gap-1">
              <RefreshCw className="h-3 w-3" /> fallback ×{result.fallback_count}
            </span>
          )}
          {result.provider_adapter && (
            <code className="font-mono text-primary">{result.provider_adapter}</code>
          )}
        </div>
      </div>
      <div className="p-4 text-sm leading-relaxed whitespace-pre-wrap">{result.content}</div>
    </Card>
  );
}
