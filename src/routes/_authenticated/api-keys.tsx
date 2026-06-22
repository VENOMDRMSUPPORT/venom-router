import { createFileRoute } from "@tanstack/react-router";
import { useSuspenseQuery, queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { useServerFn } from "@tanstack/react-start";
import { Suspense, useState } from "react";
import { Header } from "@/components/layout/header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Key, KeyRound, Plus, Copy, Check, Ban, Trash2, AlertTriangle } from "lucide-react";
import { toast } from "sonner";
import { listApiKeys, createApiKey, revokeApiKey, deleteApiKey } from "@/lib/venom.functions";

export const Route = createFileRoute("/_authenticated/api-keys")({
  head: () => ({ meta: [{ title: "API Keys — Venom Router" }] }),
  component: ApiKeysPage,
});

type VenomSlug = "lite" | "pro" | "max";

function ApiKeysPage() {
  return (
    <>
      <Header
        title="API Keys"
        description="Issue keys for external projects calling /v1/chat/completions."
        icon={<Key className="h-5 w-5" />}
        actions={<CreateKeyButton />}
      />
      <div className="flex-1 overflow-y-auto px-4 sm:px-6 py-6">
        <Suspense fallback={<Skeleton className="h-48 rounded-2xl" />}>
          <ApiKeysList />
        </Suspense>
      </div>
    </>
  );
}

function ApiKeysList() {
  const fn = useServerFn(listApiKeys);
  const { data } = useSuspenseQuery(queryOptions({ queryKey: ["api-keys"], queryFn: () => fn() }));
  if (data.length === 0) {
    return (
      <Card className="border-border/60">
        <CardContent className="p-12 text-center space-y-3">
          <KeyRound className="size-10 mx-auto text-muted-foreground" />
          <p className="font-medium">No keys yet</p>
          <p className="text-sm text-muted-foreground">
            Create your first Venom API key to wire up external projects.
          </p>
        </CardContent>
      </Card>
    );
  }
  return (
    <div className="space-y-2">
      {data.map((k) => (
        <KeyRow key={k.id} k={k} />
      ))}
    </div>
  );
}

type KeyRowData = Awaited<ReturnType<typeof listApiKeys>>[number];

function KeyRow({ k }: { k: KeyRowData }) {
  const qc = useQueryClient();
  const revokeFn = useServerFn(revokeApiKey);
  const deleteFn = useServerFn(deleteApiKey);
  const revoke = useMutation({
    mutationFn: () => revokeFn({ data: { id: k.id } }),
    onSuccess: () => {
      toast.success("Key revoked");
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });
  const del = useMutation({
    mutationFn: () => deleteFn({ data: { id: k.id } }),
    onSuccess: () => {
      toast.success("Key deleted");
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  return (
    <Card className="border-border/60">
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-base">{k.name}</CardTitle>
        <div className="flex items-center gap-2">
          {k.revoked_at ? (
            <Badge variant="destructive">revoked</Badge>
          ) : (
            <Badge className="bg-primary/20 text-primary border-primary/30">active</Badge>
          )}
          {!k.revoked_at && (
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button variant="outline" size="sm">
                  <Ban className="size-3.5" /> Revoke
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>Revoke this key?</AlertDialogTitle>
                  <AlertDialogDescription>
                    Any project using <span className="font-mono">{k.key_prefix}…</span> will
                    immediately fail with 401. This cannot be undone.
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>Cancel</AlertDialogCancel>
                  <AlertDialogAction onClick={() => revoke.mutate()}>Revoke</AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          )}
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="ghost" size="sm">
                <Trash2 className="size-3.5" />
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Delete this key permanently?</AlertDialogTitle>
                <AlertDialogDescription>
                  The row will be removed. Usage records linked to it are kept (FK is nullable).
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction onClick={() => del.mutate()}>Delete</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </CardHeader>
      <CardContent className="text-sm grid grid-cols-2 md:grid-cols-5 gap-3 text-muted-foreground">
        <div>
          <p className="text-xs uppercase">Prefix</p>
          <p className="font-mono text-foreground">{k.key_prefix}…</p>
        </div>
        <div>
          <p className="text-xs uppercase">Allowed</p>
          <p className="font-mono text-foreground">{k.allowed_models.join(", ")}</p>
        </div>
        <div>
          <p className="text-xs uppercase">RPM</p>
          <p className="tabular-nums text-foreground">{k.rpm_limit ?? "∞"}</p>
        </div>
        <div>
          <p className="text-xs uppercase">TPD</p>
          <p className="tabular-nums text-foreground">{k.tpd_limit ?? "∞"}</p>
        </div>
        <div>
          <p className="text-xs uppercase">Monthly cap</p>
          <p className="tabular-nums text-foreground">
            {k.monthly_cap_usd ? `$${k.monthly_cap_usd}` : "∞"}
          </p>
        </div>
      </CardContent>
    </Card>
  );
}

const ALL_MODELS: VenomSlug[] = ["lite", "pro", "max"];

function CreateKeyButton() {
  const qc = useQueryClient();
  const createFn = useServerFn(createApiKey);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [allowed, setAllowed] = useState<VenomSlug[]>(["lite", "pro", "max"]);
  const [rpm, setRpm] = useState<string>("");
  const [tpd, setTpd] = useState<string>("");
  const [cap, setCap] = useState<string>("");
  const [revealed, setRevealed] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const m = useMutation({
    mutationFn: () =>
      createFn({
        data: {
          name: name.trim(),
          allowed_models: allowed,
          rpm_limit: rpm ? +rpm : null,
          tpd_limit: tpd ? +tpd : null,
          monthly_cap_usd: cap ? +cap : null,
        },
      }),
    onSuccess: (res) => {
      setRevealed(res.raw);
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  function reset() {
    setName("");
    setAllowed(["lite", "pro", "max"]);
    setRpm("");
    setTpd("");
    setCap("");
    setRevealed(null);
    setCopied(false);
  }

  function toggle(slug: VenomSlug) {
    setAllowed((p) => (p.includes(slug) ? p.filter((s) => s !== slug) : [...p, slug]));
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) reset();
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="size-4" /> Create key
        </Button>
      </DialogTrigger>
      <DialogContent>
        {revealed ? (
          <>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <AlertTriangle className="size-5 text-[color:var(--color-warning)]" />
                Copy this key now
              </DialogTitle>
              <DialogDescription>
                This is the only time the raw key will ever be shown. Only its SHA-256 hash is
                stored.
              </DialogDescription>
            </DialogHeader>
            <div className="flex items-center gap-2 rounded-md border border-border bg-muted/40 p-3">
              <code className="flex-1 text-xs font-mono break-all">{revealed}</code>
              <Button
                variant="outline"
                size="sm"
                onClick={async () => {
                  await navigator.clipboard.writeText(revealed);
                  setCopied(true);
                  toast.success("Copied to clipboard");
                }}
              >
                {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                {copied ? "Copied" : "Copy"}
              </Button>
            </div>
            <DialogFooter>
              <Button
                onClick={() => {
                  setOpen(false);
                  reset();
                }}
              >
                I've saved it
              </Button>
            </DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>Create API key</DialogTitle>
              <DialogDescription>Set scope and limits. Defaults mean no limit.</DialogDescription>
            </DialogHeader>
            <div className="space-y-3">
              <div className="space-y-1">
                <Label>Name</Label>
                <Input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="e.g. acme-prod-backend"
                  maxLength={80}
                />
              </div>
              <div className="space-y-2">
                <Label>Allowed Venom models</Label>
                <div className="flex gap-4">
                  {ALL_MODELS.map((s) => (
                    <label
                      key={s}
                      className="flex items-center gap-2 cursor-pointer text-sm font-mono"
                    >
                      <Checkbox checked={allowed.includes(s)} onCheckedChange={() => toggle(s)} />
                      venom/{s}
                    </label>
                  ))}
                </div>
              </div>
              <div className="grid grid-cols-3 gap-3">
                <div className="space-y-1">
                  <Label>RPM</Label>
                  <Input
                    type="number"
                    min={1}
                    value={rpm}
                    onChange={(e) => setRpm(e.target.value)}
                    placeholder="∞"
                  />
                </div>
                <div className="space-y-1">
                  <Label>TPD</Label>
                  <Input
                    type="number"
                    min={1}
                    value={tpd}
                    onChange={(e) => setTpd(e.target.value)}
                    placeholder="∞"
                  />
                </div>
                <div className="space-y-1">
                  <Label>Monthly $</Label>
                  <Input
                    type="number"
                    min={1}
                    value={cap}
                    onChange={(e) => setCap(e.target.value)}
                    placeholder="∞"
                  />
                </div>
              </div>
            </div>
            <DialogFooter>
              <Button variant="ghost" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button
                disabled={!name.trim() || allowed.length === 0 || m.isPending}
                onClick={() => m.mutate()}
              >
                Create key
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
