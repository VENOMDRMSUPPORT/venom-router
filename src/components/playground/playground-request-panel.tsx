import { Loader2, Send } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { VenomSlug } from "./playground-types";

type Props = {
  selectedModel: VenomSlug;
  onModelChange: (slug: VenomSlug) => void;
  prompt: string;
  onPromptChange: (value: string) => void;
  maxTokens: string;
  onMaxTokensChange: (value: string) => void;
  temperature: string;
  onTemperatureChange: (value: string) => void;
  loading: boolean;
  onSend: () => void;
};

export function PlaygroundRequestPanel({
  selectedModel,
  onModelChange,
  prompt,
  onPromptChange,
  maxTokens,
  onMaxTokensChange,
  temperature,
  onTemperatureChange,
  loading,
  onSend,
}: Props) {
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2 flex-wrap">
        <Badge variant="outline" className="font-mono text-[10px]">
          POST /api/v1/chat/completions
        </Badge>
        <span className="text-[11px] text-muted-foreground">via dashboard proxy</span>
      </div>

      <div className="flex gap-2">
        {(["lite", "pro", "max"] as const).map((slug) => (
          <button
            key={slug}
            type="button"
            onClick={() => onModelChange(slug)}
            className={cn(
              "flex-1 rounded-lg border py-2 text-xs font-mono font-semibold transition-all",
              selectedModel === slug
                ? "border-primary/50 bg-primary/10 text-primary"
                : "border-border/50 bg-background/50 text-muted-foreground hover:border-border",
            )}
          >
            venom/{slug}
          </button>
        ))}
      </div>

      <div className="rounded-2xl border border-border bg-card p-4 space-y-3">
        <Textarea
          placeholder="Enter your prompt…"
          value={prompt}
          onChange={(e) => onPromptChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) onSend();
          }}
          className="min-h-[160px] resize-none border-0 bg-transparent p-0 focus-visible:ring-0 text-sm"
        />
        <div className="grid grid-cols-2 gap-3 border-t border-border/50 pt-3">
          <div className="space-y-1">
            <label className="text-[10px] uppercase tracking-wider text-muted-foreground">
              max_tokens
            </label>
            <Input
              type="number"
              placeholder="optional"
              value={maxTokens}
              onChange={(e) => onMaxTokensChange(e.target.value)}
              className="h-8 text-xs font-mono"
            />
          </div>
          <div className="space-y-1">
            <label className="text-[10px] uppercase tracking-wider text-muted-foreground">
              temperature
            </label>
            <Input
              type="number"
              step="0.1"
              min="0"
              max="2"
              placeholder="optional"
              value={temperature}
              onChange={(e) => onTemperatureChange(e.target.value)}
              className="h-8 text-xs font-mono"
            />
          </div>
        </div>
        <div className="flex items-center justify-between border-t border-border/50 pt-3">
          <span className="text-[11px] text-muted-foreground">⌘Enter or Ctrl+Enter to send</span>
          <Button size="sm" onClick={onSend} disabled={loading || !prompt.trim()}>
            {loading ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> Routing…
              </>
            ) : (
              <>
                <Send className="h-3.5 w-3.5" /> Send
              </>
            )}
          </Button>
        </div>
      </div>
    </div>
  );
}
