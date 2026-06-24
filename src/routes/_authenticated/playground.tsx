import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Suspense, useState, useMemo, useRef, useEffect } from "react";
import {
  FlaskConical,
  History,
  Send,
  Trash2,
  Sliders,
  Play,
  Code,
  Terminal,
  Eye,
  Sparkles,
  AlertCircle,
} from "lucide-react";
import { Header } from "@/components/layout/header";
import { Skeleton } from "@/components/ui/skeleton";
import { PageControls, type DebugEntry } from "@/components/layout/page-controls";
import { PlaygroundInspector } from "@/components/playground/playground-inspector";
import { PlaygroundRoutingTrace } from "@/components/playground/playground-routing-trace";
import { PlaygroundCopyMenu } from "@/components/playground/playground-copy-menu";
import { PlaygroundHistoryTab } from "@/components/playground/playground-history-tab";
import type {
  PlaygroundRequest,
  PlaygroundResponse,
  PlaygroundErrorPayload,
  VenomSlug,
} from "@/components/playground/playground-types";
import { TIER_META } from "@/components/routing/routing-constants";
import { api } from "@/lib/api-client";
import type { RoutingRule } from "@/lib/db/venom.server";
import { supabase } from "@/integrations/supabase/client";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import type { RoutingTrace } from "@/lib/routing/types";

export const Route = createFileRoute("/_authenticated/playground")({
  head: () => ({ meta: [{ title: "Playground — Venom Router" }] }),
  component: PlaygroundPage,
});

async function postPlaygroundChat(req: PlaygroundRequest): Promise<PlaygroundResponse> {
  const {
    data: { session },
  } = await supabase.auth.getSession();
  if (!session?.access_token) throw new Error("Not authenticated");

  const res = await fetch("/api/dashboard/playground/chat", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${session.access_token}`,
    },
    body: JSON.stringify(req),
  });

  const body = (await res.json()) as PlaygroundResponse | PlaygroundErrorPayload;
  if (!res.ok) {
    const errPayload = body as PlaygroundErrorPayload;
    const err = new Error(
      errPayload.error_message ?? errPayload.error ?? "Request failed",
    ) as Error & { payload?: PlaygroundErrorPayload };
    err.payload = errPayload;
    throw err;
  }
  return body as PlaygroundResponse;
}

interface Message {
  role: "user" | "assistant" | "system";
  content: string;
  trace?: RoutingTrace | null;
  latency_ms?: number;
  cost_usd?: number;
  tokens?: number;
  model_used?: string | null;
  error_code?: string;
  timestamp: number;
}

function PlaygroundPage() {
  const [activeTab, setActiveTab] = useState("playground");
  const [debugLog, setDebugLog] = useState<DebugEntry[]>([]);

  const tabs = [
    {
      id: "playground",
      label: "Playground",
      icon: <FlaskConical className="h-3.5 w-3.5" />,
    },
    {
      id: "history",
      label: "History",
      icon: <History className="h-3.5 w-3.5" />,
    },
  ];

  return (
    <>
      <Header
        title="Playground"
        description="Test prompts directly against the routing engine."
        icon={<FlaskConical className="h-5 w-5" />}
      />
      <div className="flex-1 overflow-hidden flex flex-col bg-background/30">
        <div className="px-6 pt-6 flex-shrink-0">
          <PageControls
            breadcrumbs={["Dashboard", "Testing", "Playground"]}
            debugLog={debugLog}
            onClearDebug={() => setDebugLog([])}
            tabs={tabs}
            activeTab={activeTab}
            onTabChange={setActiveTab}
          />
        </div>

        {activeTab === "playground" ? (
          <div className="flex-1 flex overflow-hidden">
            <PlaygroundWorkspace setDebugLog={setDebugLog} />
          </div>
        ) : (
          <div className="flex-1 overflow-y-auto px-6 pb-6 scrollbar-app">
            <Suspense fallback={<Skeleton className="h-64 rounded-2xl" />}>
              <PlaygroundHistoryTab />
            </Suspense>
          </div>
        )}
      </div>
    </>
  );
}

function PlaygroundWorkspace({
  setDebugLog,
}: {
  setDebugLog: React.Dispatch<React.SetStateAction<DebugEntry[]>>;
}) {
  const [selectedModel, setSelectedModel] = useState<VenomSlug>("pro");
  const [systemPrompt, setSystemPrompt] = useState("");
  const [prompt, setPrompt] = useState("");
  const [maxTokens, setMaxTokens] = useState("");
  const [temperature, setTemperature] = useState("");
  const [loading, setLoading] = useState(false);
  const [messages, setMessages] = useState<Message[]>([]);
  const [selectedMsgIndex, setSelectedMsgIndex] = useState<number | null>(null);
  const [rightPanelTab, setRightPanelTab] = useState<"trace" | "json">("trace");

  const qc = useQueryClient();
  const chatEndRef = useRef<HTMLDivElement>(null);

  const { data: routingRules = [] } = useQuery({
    queryKey: ["routing-rules"],
    queryFn: () => api.get<RoutingRule[]>("/api/dashboard/routing-rules"),
  });

  const activeRulesByTier = useMemo(() => {
    return Object.fromEntries(
      (["lite", "pro", "max"] as const).map((tier) => [
        tier,
        routingRules.filter((r) => r.venom_slug === tier && r.active).length,
      ]),
    ) as Record<VenomSlug, number>;
  }, [routingRules]);

  const selectedTierHasRules = activeRulesByTier[selectedModel] > 0;

  // Scroll to bottom of chat
  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, loading]);

  const activeTrace = useMemo(() => {
    if (selectedMsgIndex !== null && messages[selectedMsgIndex]) {
      return messages[selectedMsgIndex].trace ?? null;
    }
    // Fallback to latest assistant message with trace
    for (let i = messages.length - 1; i >= 0; i--) {
      if (messages[i].role === "assistant" && messages[i].trace) {
        return messages[i].trace ?? null;
      }
    }
    return null;
  }, [messages, selectedMsgIndex]);

  const inspectorRequest = useMemo((): PlaygroundRequest | null => {
    // Return request payload for the selected trace or latest
    const activeMsg = selectedMsgIndex !== null ? messages[selectedMsgIndex] : null;
    if (activeMsg && activeMsg.role === "assistant") {
      // reconstruct what was sent
      const idx = messages.indexOf(activeMsg);
      const priorMessages = messages.slice(0, idx).map((m) => ({
        role: m.role,
        content: m.content,
      }));
      const req: PlaygroundRequest = {
        venom_slug: selectedModel,
        messages: priorMessages,
      };
      const mt = maxTokens.trim() ? Number(maxTokens) : undefined;
      const temp = temperature.trim() ? Number(temperature) : undefined;
      if (mt != null) req.max_tokens = mt;
      if (temp != null) req.temperature = temp;
      return req;
    }

    // Default pending request
    if (!prompt.trim() && messages.length === 0) return null;
    const historyMsgs = messages.map((m) => ({ role: m.role, content: m.content }));
    const activeContent = prompt.trim() || "...";
    const req: PlaygroundRequest = {
      venom_slug: selectedModel,
      messages: [
        ...(systemPrompt.trim() ? [{ role: "system" as const, content: systemPrompt.trim() }] : []),
        ...historyMsgs,
        ...(prompt.trim() ? [{ role: "user" as const, content: activeContent }] : []),
      ],
    };
    const mt = maxTokens.trim() ? Number(maxTokens) : undefined;
    const temp = temperature.trim() ? Number(temperature) : undefined;
    if (mt != null && !Number.isNaN(mt)) req.max_tokens = mt;
    if (temp != null && !Number.isNaN(temp)) req.temperature = temp;
    return req;
  }, [messages, selectedMsgIndex, selectedModel, prompt, systemPrompt, maxTokens, temperature]);

  async function send() {
    const text = prompt.trim();
    if (!text || loading || !selectedTierHasRules) return;

    // 1. Add user message
    const userMessage: Message = {
      role: "user",
      content: text,
      timestamp: Date.now(),
    };
    const updatedMessages = [...messages, userMessage];
    setMessages(updatedMessages);
    setPrompt("");
    setLoading(true);

    const t0 = Date.now();
    const dbId = `${t0}-${Math.random().toString(36).slice(2)}`;

    // Prepare API messages payload
    const apiMessages = [
      ...(systemPrompt.trim() ? [{ role: "system" as const, content: systemPrompt.trim() }] : []),
      ...updatedMessages.map((m) => ({ role: m.role, content: m.content })),
    ];

    const reqPayload: PlaygroundRequest = {
      venom_slug: selectedModel,
      messages: apiMessages,
    };
    const mt = maxTokens.trim() ? Number(maxTokens) : undefined;
    const temp = temperature.trim() ? Number(temperature) : undefined;
    if (mt != null && !Number.isNaN(mt)) reqPayload.max_tokens = mt;
    if (temp != null && !Number.isNaN(temp)) reqPayload.temperature = temp;

    setDebugLog((prev) => [
      {
        id: dbId,
        ts: t0,
        op: "playground/chat",
        label: `venom/${selectedModel}`,
        req: reqPayload,
        status: "pending",
      },
      ...prev.slice(0, 49),
    ]);

    try {
      const res = await postPlaygroundChat(reqPayload);

      // 2. Add assistant message
      const assistantMessage: Message = {
        role: "assistant",
        content: res.content,
        trace: res.trace,
        latency_ms: res.latency_ms,
        cost_usd: res.cost_usd,
        tokens: res.input_tokens + res.output_tokens,
        model_used: res.provider_adapter,
        timestamp: Date.now(),
      };
      setMessages((prev) => [...prev, assistantMessage]);
      setSelectedMsgIndex(null); // Clear selected, auto-pointing to latest

      setDebugLog((prev) =>
        prev.map((e) =>
          e.id === dbId ? { ...e, res, ms: Date.now() - t0, status: "success" } : e,
        ),
      );
      qc.invalidateQueries({ queryKey: ["usage-analytics"] });
    } catch (e: unknown) {
      const err = e as Error & { payload?: PlaygroundErrorPayload };
      const displayMsg = err.payload?.error_message ?? err.message ?? "Request failed";
      toast.error(displayMsg);

      const failMessage: Message = {
        role: "assistant",
        content: displayMsg,
        error_code: err.payload?.error_code,
        trace: err.payload?.trace ?? null,
        timestamp: Date.now(),
      };
      setMessages((prev) => [...prev, failMessage]);

      setDebugLog((prev) =>
        prev.map((entry) =>
          entry.id === dbId
            ? { ...entry, err: displayMsg, res: err.payload, ms: Date.now() - t0, status: "error" }
            : entry,
        ),
      );
    } finally {
      setLoading(false);
    }
  }

  function clearChat() {
    setMessages([]);
    setPrompt("");
    setSelectedMsgIndex(null);
    toast.success("Conversation reset");
  }

  // Pre-fill prompt templates
  const starterPrompts = [
    { title: "Test Routing Code", text: "Write an optimized TypeScript binary search function." },
    { title: "Test Fallback", text: "Explain quantum computing in simple terms for a child." },
    {
      title: "Complex Reasoning",
      text: "If a pool is filled by two pipes in 4 hours, and pipe A alone takes 6 hours, how long does pipe B take?",
    },
  ];

  const maxTokensNum = maxTokens.trim() ? Number(maxTokens) : undefined;
  const temperatureNum = temperature.trim() ? Number(temperature) : undefined;

  return (
    <div className="flex-1 grid grid-cols-1 lg:grid-cols-12 gap-0 border-t border-border/50 h-full overflow-hidden">
      {/* 1. Left Config Column */}
      <div className="lg:col-span-3 border-r border-border/50 p-5 bg-card/15 overflow-y-auto space-y-5 flex flex-col justify-between scrollbar-app">
        <div className="space-y-4">
          <div className="flex items-center justify-between pb-1.5 border-b border-border/30">
            <h3 className="text-xs font-bold font-display uppercase tracking-wider text-foreground flex items-center gap-1.5">
              <Sliders className="h-3.5 w-3.5 text-primary" />
              Parameters
            </h3>
            <PlaygroundCopyMenu
              venomSlug={selectedModel}
              prompt={
                prompt || (messages.length > 0 ? messages[messages.length - 1].content : "Hello")
              }
              maxTokens={maxTokensNum}
              temperature={temperatureNum}
            />
          </div>

          {/* Model selection */}
          <div className="space-y-1.5">
            <Label className="text-[11px] font-semibold text-foreground">Venom Routing Tier</Label>
            <div className="grid grid-cols-3 gap-1 bg-background/50 border border-border/50 rounded-lg p-0.5 shadow-sm">
              {(["lite", "pro", "max"] as const).map((slug) => (
                <button
                  key={slug}
                  type="button"
                  onClick={() => setSelectedModel(slug)}
                  className={`relative rounded-md py-1.5 text-[10px] font-mono font-bold transition-all cursor-pointer
                    ${
                      selectedModel === slug
                        ? "bg-primary text-primary-foreground shadow-sm"
                        : "text-muted-foreground hover:text-foreground hover:bg-muted/30"
                    }
                  `}
                >
                  {slug}
                  {activeRulesByTier[slug] === 0 && (
                    <span
                      className="absolute top-0.5 right-1 h-1.5 w-1.5 rounded-full bg-amber-400"
                      title="No active routing rules"
                    />
                  )}
                </button>
              ))}
            </div>
          </div>

          {!selectedTierHasRules && (
            <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-2.5 flex items-start gap-2">
              <AlertCircle className="h-3.5 w-3.5 text-amber-400 shrink-0 mt-0.5" />
              <p className="text-[10px] text-amber-300/90 leading-relaxed">
                No active routing rules for {TIER_META[selectedModel].label}.{" "}
                <Link to="/routing" className="underline underline-offset-2 hover:text-amber-200">
                  Configure rules
                </Link>{" "}
                before sending requests.
              </p>
            </div>
          )}

          {/* System prompt */}
          <div className="space-y-1.5">
            <Label className="text-[11px] font-semibold text-foreground">System Prompt</Label>
            <Textarea
              placeholder="Inject instructions, context, or rules for the routing engine…"
              value={systemPrompt}
              onChange={(e) => setSystemPrompt(e.target.value)}
              className="min-h-[100px] text-xs leading-relaxed resize-y bg-background/50 border-border/60 focus-visible:ring-1 focus-visible:ring-primary/40 focus-visible:border-primary/50"
            />
          </div>

          {/* Temperature */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label className="text-[11px] font-semibold text-foreground">Temperature</Label>
              <span className="text-[10px] font-mono font-bold text-muted-foreground bg-muted/50 px-1.5 py-0.5 rounded">
                {temperature.trim() ? temperature : "default"}
              </span>
            </div>
            <Input
              type="number"
              step="0.1"
              min="0"
              max="2"
              placeholder="optional (e.g. 0.7)"
              value={temperature}
              onChange={(e) => setTemperature(e.target.value)}
              className="h-9 text-xs font-mono bg-background/50"
            />
          </div>

          {/* Max Tokens */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label className="text-[11px] font-semibold text-foreground">Max Tokens</Label>
              <span className="text-[10px] font-mono font-bold text-muted-foreground bg-muted/50 px-1.5 py-0.5 rounded">
                {maxTokens.trim() ? maxTokens : "default"}
              </span>
            </div>
            <Input
              type="number"
              placeholder="optional (e.g. 2048)"
              value={maxTokens}
              onChange={(e) => setMaxTokens(e.target.value)}
              className="h-9 text-xs font-mono bg-background/50"
            />
          </div>
        </div>

        <Button
          variant="outline"
          size="sm"
          onClick={clearChat}
          disabled={messages.length === 0}
          className="w-full gap-1.5 text-xs text-muted-foreground hover:text-destructive hover:border-destructive/30 hover:bg-destructive/5 transition-colors border-border/60"
        >
          <Trash2 className="h-3.5 w-3.5" />
          Clear Conversation
        </Button>
      </div>

      {/* 2. Center Chat Column */}
      <div className="lg:col-span-6 flex flex-col h-full bg-background/10 overflow-hidden relative">
        {/* Chat Messages Feed */}
        <ScrollArea className="flex-1 px-6 py-6 overflow-y-auto">
          {messages.length === 0 ? (
            <div className="h-[400px] flex flex-col items-center justify-center text-center space-y-6 max-w-md mx-auto">
              <div className="h-12 w-12 rounded-full bg-primary/10 flex items-center justify-center border border-primary/20 shadow-glow">
                <FlaskConical className="h-5 w-5 text-primary animate-pulse" />
              </div>
              <div className="space-y-2">
                <h4 className="text-sm font-bold tracking-tight font-display">
                  Venom Chat Playground
                </h4>
                <p className="text-xs text-muted-foreground leading-relaxed">
                  Send multiple turns to test Venom's routing strategies, live provider fallback
                  mechanisms, and caching rules.
                </p>
              </div>

              <div className="grid grid-cols-1 gap-2.5 w-full pt-2">
                {starterPrompts.map((s, idx) => (
                  <button
                    key={idx}
                    onClick={() => setPrompt(s.text)}
                    className="text-left p-3 rounded-lg border border-border/50 bg-card/25 hover:bg-card/60 hover:border-primary/30 transition-all text-xs font-medium cursor-pointer flex items-center justify-between group"
                  >
                    <span>{s.title}</span>
                    <span className="text-[10px] text-muted-foreground group-hover:text-primary transition-colors flex items-center gap-0.5">
                      Use prompt <Play className="h-2 w-2 shrink-0 fill-current" />
                    </span>
                  </button>
                ))}
              </div>
            </div>
          ) : (
            <div className="space-y-4 pb-4">
              {messages.map((m, idx) => {
                const isUser = m.role === "user";
                const isSelected = selectedMsgIndex === idx;

                return (
                  <div
                    key={idx}
                    className={`flex flex-col w-full ${isUser ? "items-end" : "items-start"}`}
                  >
                    <div
                      onClick={() => !isUser && setSelectedMsgIndex(idx)}
                      className={`max-w-[85%] rounded-2xl px-4 py-3 text-xs leading-relaxed transition-all cursor-pointer relative group
                        ${
                          isUser
                            ? "bg-primary/95 text-primary-foreground shadow-sm hover:bg-primary"
                            : `border border-border/60 bg-card/50 text-foreground hover:bg-card/75 hover:border-primary/30
                               ${isSelected ? "ring-1 ring-primary/45 border-primary/50 shadow-glow" : "shadow-sm"}`
                        }
                      `}
                    >
                      <div className="whitespace-pre-wrap font-sans">
                        {m.content}
                        {!isUser && m.error_code === "NO_ROUTING_RULES" && (
                          <p className="mt-2 pt-2 border-t border-border/30">
                            <Link
                              to="/routing"
                              className="text-primary font-semibold hover:underline underline-offset-2"
                            >
                              Go to Routing →
                            </Link>
                          </p>
                        )}
                      </div>

                      {/* Assistant Telemetry label */}
                      {!isUser && (m.latency_ms || m.model_used) && (
                        <div className="mt-2.5 pt-2 border-t border-border/30 flex flex-wrap items-center gap-2.5 text-[9px] font-mono text-muted-foreground">
                          {m.model_used && (
                            <span className="text-primary font-bold">{m.model_used}</span>
                          )}
                          {m.latency_ms && <span>{m.latency_ms}ms</span>}
                          {m.tokens && <span>{m.tokens} tokens</span>}
                          {m.cost_usd && <span>${m.cost_usd.toFixed(5)}</span>}
                          {m.trace && (
                            <Badge
                              variant="outline"
                              className="text-[8px] h-3.5 px-1 border-primary/20 text-primary bg-primary/5 font-mono ml-auto"
                            >
                              Trace active
                            </Badge>
                          )}
                        </div>
                      )}
                    </div>
                    {!isUser && m.trace && (
                      <span className="text-[9px] text-muted-foreground/45 mt-1 px-1.5">
                        Click bubble to view execution telemetry
                      </span>
                    )}
                  </div>
                );
              })}

              {/* Typing indicator */}
              {loading && (
                <div className="flex items-start justify-start">
                  <div className="rounded-2xl border border-border/60 bg-card/45 px-4.5 py-3.5 flex items-center gap-1">
                    <span className="h-1.5 w-1.5 rounded-full bg-primary/60 animate-bounce [animation-delay:-0.3s]"></span>
                    <span className="h-1.5 w-1.5 rounded-full bg-primary/60 animate-bounce [animation-delay:-0.15s]"></span>
                    <span className="h-1.5 w-1.5 rounded-full bg-primary/60 animate-bounce"></span>
                  </div>
                </div>
              )}
              <div ref={chatEndRef} />
            </div>
          )}
        </ScrollArea>

        {/* Floating Input Box */}
        <div className="p-4 border-t border-border/50 bg-card/25 backdrop-blur-md">
          <div className="relative rounded-xl border border-border/60 bg-background/60 shadow-sm flex items-center p-1.5 gap-2">
            <Textarea
              placeholder="Ask Venom routing engine…"
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  send();
                }
              }}
              className="flex-1 min-h-[44px] max-h-[100px] resize-none border-0 bg-transparent py-2.5 px-3 focus-visible:ring-0 focus-visible:ring-offset-0 text-xs leading-relaxed scrollbar-app"
            />
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="shrink-0">
                    <Button
                      size="sm"
                      onClick={send}
                      disabled={loading || !prompt.trim() || !selectedTierHasRules}
                      className="h-8 w-8 p-0 rounded-lg shrink-0 shadow-glow"
                    >
                      <Send className="h-3.5 w-3.5" />
                    </Button>
                  </span>
                </TooltipTrigger>
                {!selectedTierHasRules && (
                  <TooltipContent side="top" className="text-xs">
                    Configure routing rules first
                  </TooltipContent>
                )}
              </Tooltip>
            </TooltipProvider>
          </div>
          <p className="text-[10px] text-muted-foreground/60 text-center mt-2 flex items-center justify-center gap-1">
            <Terminal className="h-3 w-3 shrink-0" />
            Press **Enter** to send. Use **Shift+Enter** for newlines.
          </p>
        </div>
      </div>

      {/* 3. Right Telemetry Drawer Column */}
      <div className="lg:col-span-3 border-l border-border/50 flex flex-col h-full bg-card/15 overflow-hidden">
        {/* Toggle tabs */}
        <div className="flex border-b border-border/50 bg-background/40 p-1 flex-shrink-0">
          <button
            onClick={() => setRightPanelTab("trace")}
            className={`flex-1 rounded-md py-1.5 text-[10px] font-bold uppercase tracking-wider transition-all flex items-center justify-center gap-1.5 cursor-pointer
              ${
                rightPanelTab === "trace"
                  ? "bg-card text-foreground border border-border/50 shadow-sm"
                  : "text-muted-foreground hover:text-foreground"
              }
            `}
          >
            <Sparkles className="h-3 w-3" />
            Routing Trace
          </button>
          <button
            onClick={() => setRightPanelTab("json")}
            className={`flex-1 rounded-md py-1.5 text-[10px] font-bold uppercase tracking-wider transition-all flex items-center justify-center gap-1.5 cursor-pointer
              ${
                rightPanelTab === "json"
                  ? "bg-card text-foreground border border-border/50 shadow-sm"
                  : "text-muted-foreground hover:text-foreground"
              }
            `}
          >
            <Code className="h-3 w-3" />
            JSON Request
          </button>
        </div>

        {/* Scrollable details */}
        <ScrollArea className="flex-1 overflow-y-auto p-4 scrollbar-app">
          {rightPanelTab === "trace" ? (
            activeTrace ? (
              <div className="space-y-4">
                <PlaygroundRoutingTrace trace={activeTrace} />
              </div>
            ) : (
              <div className="h-[250px] flex flex-col items-center justify-center text-center p-6 border border-dashed border-border/60 rounded-xl space-y-2.5">
                <Eye className="h-6 w-6 text-muted-foreground/35" />
                <p className="text-[11px] font-bold">No Routing Trace Available</p>
                <p className="text-[10px] text-muted-foreground leading-relaxed max-w-[200px]">
                  Submit a message to view the detailed path evaluation.
                </p>
              </div>
            )
          ) : (
            <div className="space-y-4">
              <PlaygroundInspector request={inspectorRequest} />
            </div>
          )}
        </ScrollArea>
      </div>
    </div>
  );
}
