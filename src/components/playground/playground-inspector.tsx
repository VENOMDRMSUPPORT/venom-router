import { Card } from "@/components/ui/card";
import type { PlaygroundRequest } from "./playground-types";

type Props = {
  request: PlaygroundRequest | null;
};

export function PlaygroundInspector({ request }: Props) {
  const headers = {
    "Content-Type": "application/json",
    Authorization: "Bearer <session>",
  };

  const body = request ?? {
    venom_slug: "pro",
    messages: [{ role: "user", content: "…" }],
  };

  return (
    <Card className="border-border/60 overflow-hidden">
      <div className="border-b border-border/50 px-4 py-2.5 bg-muted/20">
        <span className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
          Request Inspector
        </span>
      </div>
      <div className="p-4 space-y-3">
        <div>
          <p className="text-[10px] uppercase tracking-widest text-muted-foreground/60 font-semibold pb-1">
            Headers
          </p>
          <pre className="text-[11px] font-mono bg-muted/40 rounded-md p-2.5 overflow-x-auto text-foreground/80">
            {JSON.stringify(headers, null, 2)}
          </pre>
        </div>
        <div>
          <p className="text-[10px] uppercase tracking-widest text-muted-foreground/60 font-semibold pb-1">
            Body → POST /api/dashboard/playground/chat
          </p>
          <pre className="text-[11px] font-mono bg-muted/40 rounded-md p-2.5 overflow-x-auto text-foreground/80 max-h-48 overflow-y-auto">
            {JSON.stringify(body, null, 2)}
          </pre>
        </div>
      </div>
    </Card>
  );
}
