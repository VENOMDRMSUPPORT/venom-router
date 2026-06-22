import { useState } from "react";
import { useServerFn } from "@tanstack/react-start";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, ShieldCheck } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "sonner";
import { connectCredential } from "@/lib/providers/integrations.functions";

export function ConnectCredentialDialog({
  open,
  onOpenChange,
  providerSlug,
  providerName,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  providerSlug: string;
  providerName: string;
}) {
  const qc = useQueryClient();
  const submit = useServerFn(connectCredential);
  const [credential, setCredential] = useState("");
  const [label, setLabel] = useState("");
  const [busy, setBusy] = useState(false);

  async function go() {
    if (!credential.trim()) return;
    setBusy(true);
    try {
      const r: any = await submit({
        data: {
          provider_slug: providerSlug,
          auth_type: "api_key",
          credential: credential.trim(),
          label: label.trim() || undefined,
        },
      });
      if (r?.health?.ok === false) {
        toast.warning(`Saved, but health check failed: ${r.health.error ?? "unknown"}`);
      } else {
        toast.success(`${providerName} connected`);
      }
      await qc.invalidateQueries({ queryKey: ["integrations"] });
      setCredential("");
      setLabel("");
      onOpenChange(false);
    } catch (e: any) {
      toast.error(e?.message ?? "Failed to save credential");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Connect {providerName}</DialogTitle>
          <DialogDescription>
            Paste your API key. It is encrypted with AES-256-GCM before storage and never shown
            again.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="cred">API key</Label>
            <Textarea
              id="cred"
              rows={3}
              value={credential}
              onChange={(e) => setCredential(e.target.value)}
              placeholder="sk-…"
              className="font-mono text-xs"
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="lbl">Label (optional)</Label>
            <Input
              id="lbl"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="personal account"
            />
          </div>
          <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
            <ShieldCheck className="size-3.5" />
            Stored encrypted. A health check runs immediately after connect.
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button onClick={go} disabled={busy || !credential.trim()}>
              {busy && <Loader2 className="size-4 animate-spin" />} Save &amp; encrypt
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
