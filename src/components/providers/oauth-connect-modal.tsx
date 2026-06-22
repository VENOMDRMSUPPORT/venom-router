import { useCallback, useEffect, useRef, useState } from "react";
import { useServerFn } from "@tanstack/react-start";
import { useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Loader2, AlertCircle } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { completeOAuthFlow, startOAuthFlow } from "@/lib/providers/integrations.functions";
import { invalidateModelViews } from "@/lib/providers/sync-cache";

type Step = "waiting" | "success" | "error";

interface CallbackPayload {
  code?: string | null;
  state?: string | null;
  error?: string | null;
  errorDescription?: string | null;
}

export function OAuthConnectModal({
  open,
  onOpenChange,
  onSuccess,
  providerSlug,
  providerName,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onSuccess?: () => void;
  providerSlug: string;
  providerName: string;
}) {
  const qc = useQueryClient();
  const start = useServerFn(startOAuthFlow);
  const complete = useServerFn(completeOAuthFlow);

  const [step, setStep] = useState<Step>("waiting");
  const [error, setError] = useState<string | null>(null);
  const [flowId, setFlowId] = useState<string | null>(null);
  const popupRef = useRef<Window | null>(null);
  const openedRef = useRef(false);
  const processedRef = useRef(false);

  const handleCallback = useCallback(
    async (payload: CallbackPayload) => {
      if (processedRef.current || !flowId) return;
      if (payload.error) {
        processedRef.current = true;
        setError(payload.errorDescription || payload.error);
        setStep("error");
        return;
      }
      if (!payload.code) return;
      if (!payload.state) {
        processedRef.current = true;
        setError("OAuth state missing from callback — cannot verify CSRF token");
        setStep("error");
        return;
      }

      processedRef.current = true;
      try {
        const r: any = await complete({
          data: { flow_id: flowId, code: payload.code, state: payload.state },
        });
        if (r?.health?.ok === false) {
          toast.warning(`Connected, but sync failed: ${r.health.error ?? "unknown"}`);
        } else {
          toast.success(`${providerName} connected`);
        }
        await qc.invalidateQueries({ queryKey: ["integrations"] });
        await invalidateModelViews(qc);
        onSuccess?.();
        setStep("success");
        setTimeout(() => onOpenChange(false), 1200);
      } catch (e: any) {
        setError(e?.message ?? "OAuth failed");
        setStep("error");
      }
    },
    [complete, flowId, onOpenChange, onSuccess, providerName, qc],
  );

  const launchFlow = useCallback(async () => {
    if (!providerSlug) return;
    try {
      setError(null);
      setStep("waiting");
      processedRef.current = false;
      const redirect_uri = `${window.location.origin}/callback`;
      const r: any = await start({
        data: { provider_slug: providerSlug as "claude-code" | "antigravity", redirect_uri },
      });
      setFlowId(r.flow_id);
      popupRef.current = window.open(
        r.authorize_url,
        "oauth_popup",
        "width=600,height=700,noopener=0",
      );
      if (!popupRef.current) {
        window.open(r.authorize_url, "_blank", "noopener,noreferrer");
        toast.message("Allow popups for automatic sign-in, or complete auth in the new tab");
      }
    } catch (e: any) {
      setError(e?.message ?? "Failed to start OAuth");
      setStep("error");
    }
  }, [providerSlug, start]);

  useEffect(() => {
    if (open && providerSlug) {
      if (openedRef.current) return;
      openedRef.current = true;
      setFlowId(null);
      setError(null);
      setStep("waiting");
      launchFlow();
    } else if (!open) {
      openedRef.current = false;
      processedRef.current = false;
      popupRef.current?.close();
    }
  }, [open, providerSlug, launchFlow]);

  useEffect(() => {
    if (!flowId) return;
    processedRef.current = false;

    const onMessage = (event: MessageEvent) => {
      const isLocalhost = event.origin.includes("localhost") || event.origin.includes("127.0.0.1");
      const isSameOrigin = event.origin === window.location.origin;
      if (!isLocalhost && !isSameOrigin) return;
      if (event.data?.type === "oauth_callback") {
        handleCallback(event.data.data as CallbackPayload);
      }
    };
    window.addEventListener("message", onMessage);

    let channel: BroadcastChannel | undefined;
    try {
      channel = new BroadcastChannel("oauth_callback");
      channel.onmessage = (event) => handleCallback(event.data as CallbackPayload);
    } catch {
      /* ignore */
    }

    const onStorage = (event: StorageEvent) => {
      if (event.key === "oauth_callback" && event.newValue) {
        try {
          const data = JSON.parse(event.newValue);
          handleCallback(data);
          localStorage.removeItem("oauth_callback");
        } catch {
          /* ignore */
        }
      }
    };
    window.addEventListener("storage", onStorage);

    try {
      const stored = localStorage.getItem("oauth_callback");
      if (stored) {
        const data = JSON.parse(stored);
        if (data.timestamp && Date.now() - data.timestamp < 30_000) {
          handleCallback(data);
        }
        localStorage.removeItem("oauth_callback");
      }
    } catch {
      /* ignore */
    }

    return () => {
      window.removeEventListener("message", onMessage);
      window.removeEventListener("storage", onStorage);
      channel?.close();
    };
  }, [flowId, handleCallback]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Connect {providerName}</DialogTitle>
          <DialogDescription>
            Sign in via the popup window. No manual codes required — we complete the connection
            automatically.
          </DialogDescription>
        </DialogHeader>

        {step === "waiting" && (
          <div className="flex flex-col items-center gap-3 py-6 text-center">
            <Loader2 className="size-8 animate-spin text-primary" />
            <p className="text-sm text-muted-foreground">Waiting for authorization in popup…</p>
            <Button variant="outline" size="sm" onClick={launchFlow}>
              Re-open sign-in window
            </Button>
          </div>
        )}

        {step === "success" && (
          <div className="flex flex-col items-center gap-3 py-6 text-center">
            <CheckCircle2 className="size-10 text-emerald-400" />
            <p className="font-medium">Connected successfully</p>
          </div>
        )}

        {step === "error" && (
          <div className="flex flex-col items-center gap-3 py-6 text-center">
            <AlertCircle className="size-10 text-destructive" />
            <p className="text-sm text-destructive">{error}</p>
            <Button onClick={launchFlow}>Try again</Button>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
