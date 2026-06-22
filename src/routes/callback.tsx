import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";

export const Route = createFileRoute("/callback")({
  ssr: false,
  component: OAuthCallbackPage,
});

function OAuthCallbackPage() {
  const [status, setStatus] = useState<"processing" | "success" | "done" | "manual">("processing");

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const code = params.get("code");
    const state = params.get("state");
    const error = params.get("error");
    const errorDescription = params.get("error_description");

    const callbackData = { code, state, error, errorDescription, fullUrl: window.location.href };

    const expectedOrigins = [window.location.origin];

    if (window.opener) {
      for (const origin of expectedOrigins) {
        try {
          window.opener.postMessage({ type: "oauth_callback", data: callbackData }, origin);
        } catch {
          /* ignore */
        }
      }
    }

    try {
      const channel = new BroadcastChannel("oauth_callback");
      channel.postMessage(callbackData);
      channel.close();
    } catch {
      /* ignore */
    }

    try {
      localStorage.setItem(
        "oauth_callback",
        JSON.stringify({ ...callbackData, timestamp: Date.now() }),
      );
    } catch {
      /* ignore */
    }

    if (!(code || error)) {
      setStatus("manual");
      return;
    }

    setStatus("success");
    const t = window.setTimeout(() => {
      window.close();
      setTimeout(() => setStatus("done"), 500);
    }, 1500);
    return () => window.clearTimeout(t);
  }, []);

  return (
    <div className="min-h-screen flex items-center justify-center bg-background px-4">
      <div className="max-w-md text-center space-y-3">
        {status === "processing" && (
          <>
            <p className="text-xs font-mono text-primary">venom://oauth</p>
            <h1 className="text-xl font-semibold">Processing authorization…</h1>
            <p className="text-sm text-muted-foreground">
              Please wait while we complete the sign-in.
            </p>
          </>
        )}
        {(status === "success" || status === "done") && (
          <>
            <p className="text-xs font-mono text-primary">venom://oauth/ok</p>
            <h1 className="text-xl font-semibold text-emerald-400">Authorization successful</h1>
            <p className="text-sm text-muted-foreground">
              {status === "success"
                ? "This window will close automatically…"
                : "You can close this tab now."}
            </p>
          </>
        )}
        {status === "manual" && (
          <>
            <p className="text-xs font-mono text-amber-400">venom://oauth/manual</p>
            <h1 className="text-xl font-semibold">Copy this URL</h1>
            <p className="text-sm text-muted-foreground break-all font-mono bg-muted/50 p-3 rounded-md">
              {typeof window !== "undefined" ? window.location.href : ""}
            </p>
          </>
        )}
      </div>
    </div>
  );
}
