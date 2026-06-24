import { useState, Fragment } from "react";
import { Bug, Clock, Loader2, CheckCircle2, AlertCircle, ChevronRight, Trash2 } from "lucide-react";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface DebugEntry {
  id: string;
  ts: number;
  op: string;
  label: string;
  req: unknown;
  res?: unknown;
  err?: string;
  ms?: number;
  status: "pending" | "success" | "error";
}

export interface TabItem {
  id: string;
  label: string;
  count?: number | string;
  icon?: React.ReactNode;
}

interface PageControlsProps {
  breadcrumbs: string[];
  debugLog?: DebugEntry[];
  onClearDebug?: () => void;
  tabs?: TabItem[];
  activeTab?: string;
  onTabChange?: (id: string) => void;
}

function DebugEntryCard({ entry }: { entry: DebugEntry }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="rounded-lg border bg-card text-sm overflow-hidden border-border/60">
      <div
        className="flex items-center gap-2 px-3 py-2.5 cursor-pointer hover:bg-muted/40 transition-colors select-none"
        onClick={() => setExpanded((v) => !v)}
      >
        {entry.status === "pending" && (
          <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-amber-500" />
        )}
        {entry.status === "success" && (
          <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-emerald-500" />
        )}
        {entry.status === "error" && <AlertCircle className="h-3.5 w-3.5 shrink-0 text-red-500" />}
        <Badge variant="outline" className="font-mono text-[10px] shrink-0 px-1.5 py-0">
          {entry.op}
        </Badge>
        <span className="flex-1 min-w-0 text-[11px] text-muted-foreground truncate">
          {entry.label}
        </span>
        {entry.ms != null && (
          <span className="text-[10px] font-mono text-muted-foreground/60 shrink-0">
            {entry.ms}ms
          </span>
        )}
        <span className="text-[10px] text-muted-foreground/50 shrink-0 tabular-nums">
          {new Date(entry.ts).toLocaleTimeString()}
        </span>
        <ChevronRight
          className={cn(
            "h-3.5 w-3.5 shrink-0 text-muted-foreground/40 transition-transform duration-150",
            expanded && "rotate-90",
          )}
        />
      </div>
      {expanded && (
        <div className="border-t border-border/40 px-3 pb-3 space-y-2 bg-muted/20">
          <div>
            <p className="text-[10px] uppercase tracking-widest text-muted-foreground/50 font-semibold pt-2.5 pb-1">
              Request
            </p>
            <pre className="text-[11px] font-mono bg-muted/60 rounded-md p-2.5 overflow-x-auto text-foreground/80 whitespace-pre-wrap break-all leading-relaxed max-h-60 overflow-y-auto">
              {JSON.stringify(entry.req, null, 2)}
            </pre>
          </div>
          {entry.res != null && (
            <div>
              <p className="text-[10px] uppercase tracking-widest text-muted-foreground/50 font-semibold pb-1">
                Response
              </p>
              <pre className="text-[11px] font-mono bg-emerald-500/5 border border-emerald-500/15 rounded-md p-2.5 overflow-x-auto text-foreground/80 whitespace-pre-wrap break-all leading-relaxed max-h-60 overflow-y-auto">
                {JSON.stringify(entry.res, null, 2)}
              </pre>
            </div>
          )}
          {entry.err != null && (
            <div>
              <p className="text-[10px] uppercase tracking-widest text-muted-foreground/50 font-semibold pb-1">
                Error
              </p>
              <pre className="text-[11px] font-mono bg-red-500/5 border border-red-500/15 rounded-md p-2.5 overflow-x-auto text-red-400 whitespace-pre-wrap break-all leading-relaxed">
                {entry.err}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export function PageControls({
  breadcrumbs,
  debugLog,
  onClearDebug,
  tabs,
  activeTab,
  onTabChange,
}: PageControlsProps) {
  const [debugOpen, setDebugOpen] = useState(false);

  const hasLogs = debugLog && debugLog.length > 0;

  return (
    <div className="flex items-center justify-between gap-3 flex-wrap shrink-0 mb-6">
      {/* Left side: Breadcrumbs and Debug button */}
      <div className="flex items-center gap-2 flex-wrap">
        <div className="inline-flex items-center gap-2 rounded-lg border border-border bg-card px-3 py-1.5 text-xs text-muted-foreground shadow-sm">
          {breadcrumbs.map((b, i) => (
            <Fragment key={i}>
              {i > 0 && <span className="opacity-50">/</span>}
              <span className={cn(i === breadcrumbs.length - 1 && "text-foreground font-medium")}>
                {b}
              </span>
            </Fragment>
          ))}
        </div>

        {debugLog !== undefined && (
          <button
            type="button"
            onClick={() => setDebugOpen(true)}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-[11px] font-medium transition-colors shadow-sm cursor-pointer",
              hasLogs
                ? "border-primary/30 bg-primary/5 text-primary hover:bg-primary/10"
                : "border-border bg-card text-muted-foreground hover:text-foreground hover:bg-accent",
            )}
          >
            <Bug className="h-3.5 w-3.5" />
            Debug
            {hasLogs && (
              <span className="rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-bold tabular-nums text-primary">
                {debugLog.length}
              </span>
            )}
          </button>
        )}
      </div>

      {/* Right side: Grouped Tabs */}
      {tabs && tabs.length > 0 && (
        <div className="inline-flex items-center rounded-lg border border-border bg-card p-0.5 shadow-sm">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => onTabChange?.(t.id)}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-md px-3 h-8 text-xs font-medium transition-colors cursor-pointer",
                activeTab === t.id
                  ? "bg-background text-foreground shadow-sm border border-border"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {t.icon}
              <span>{t.label}</span>
              {t.count !== undefined && (
                <span
                  className={cn(
                    "ml-1.5 rounded-md px-1.5 py-0.5 text-[10px] font-semibold tabular-nums",
                    activeTab === t.id
                      ? "bg-primary/15 text-primary"
                      : "bg-muted text-muted-foreground",
                  )}
                >
                  {t.count}
                </span>
              )}
            </button>
          ))}
        </div>
      )}

      {/* Debug Logs Drawer (Sheet) */}
      {debugLog !== undefined && (
        <Sheet open={debugOpen} onOpenChange={setDebugOpen}>
          <SheetContent className="w-full sm:max-w-[600px] flex flex-col p-0 gap-0">
            <SheetHeader className="flex-row items-center justify-between px-5 py-3.5 border-b shrink-0 flex-wrap gap-2">
              <SheetTitle className="flex items-center gap-2 text-sm">
                <Bug className="h-4 w-4 text-primary" />
                Debug Log
                {debugLog.length > 0 && (
                  <Badge variant="outline" className="font-mono text-[10px] ml-1">
                    {debugLog.length}
                  </Badge>
                )}
              </SheetTitle>
              {onClearDebug && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={onClearDebug}
                  disabled={debugLog.length === 0}
                  className="gap-1 text-xs"
                >
                  <Trash2 className="size-3.5" />
                  Clear Log
                </Button>
              )}
            </SheetHeader>

            <ScrollArea className="flex-1">
              <div className="p-4 space-y-2">
                {debugLog.length === 0 ? (
                  <div className="flex flex-col items-center justify-center py-16 text-center">
                    <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted mb-3">
                      <Clock className="h-5 w-5 text-muted-foreground" />
                    </div>
                    <p className="text-sm font-medium">No operations logged yet</p>
                    <p className="mt-1 text-xs text-muted-foreground max-w-xs">
                      Perform actions on this page to see the request and response details here.
                    </p>
                  </div>
                ) : (
                  debugLog.map((entry) => <DebugEntryCard key={entry.id} entry={entry} />)
                )}
              </div>
            </ScrollArea>
          </SheetContent>
        </Sheet>
      )}
    </div>
  );
}
