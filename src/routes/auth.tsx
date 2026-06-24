import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { supabase } from "@/integrations/supabase/client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { toast } from "sonner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

export const Route = createFileRoute("/auth")({
  ssr: false,
  component: AuthPage,
});

function AuthPage() {
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [ownerExists, setOwnerExists] = useState<boolean | null>(null);

  useEffect(() => {
    supabase.auth.getUser().then(({ data }) => {
      if (data.user) navigate({ to: "/overview", replace: true });
    });
  }, [navigate]);

  useEffect(() => {
    fetch("/api/public/owner-exists")
      .then((res) => res.json())
      .then(({ ownerExists }) => setOwnerExists(ownerExists))
      .catch(() => setOwnerExists(true));
  }, []);

  async function signIn(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    const { error } = await supabase.auth.signInWithPassword({ email, password });
    setLoading(false);
    if (error) return toast.error(error.message);
    navigate({ to: "/overview", replace: true });
  }

  async function signUp(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    const { error } = await supabase.auth.signUp({
      email,
      password,
      options: { emailRedirectTo: `${window.location.origin}/overview` },
    });
    setLoading(false);
    if (error) return toast.error(error.message);
    toast.success("Owner account created. Sign in to continue.");
  }

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-[radial-gradient(circle_at_top,oklch(0.78_0.18_145/15%),transparent_60%)]">
      <Card className="w-full max-w-md border-border/60">
        <CardHeader className="space-y-1">
          <div className="flex items-center gap-2">
            <div className="size-8 rounded-md bg-primary/20 border border-primary/40 flex items-center justify-center">
              <span className="text-primary font-mono text-sm">V</span>
            </div>
            <CardTitle className="text-xl tracking-tight">Venom Router</CardTitle>
          </div>
          <CardDescription>
            Owner-only AI control center. The first account becomes the owner.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {ownerExists === false ? (
            <Tabs defaultValue="signin">
              <TabsList className="grid grid-cols-2 w-full">
                <TabsTrigger value="signin">Sign in</TabsTrigger>
                <TabsTrigger value="signup">Claim owner</TabsTrigger>
              </TabsList>
              <TabsContent value="signin">
                <form onSubmit={signIn} className="space-y-3 mt-4">
                  <Field label="Email" value={email} onChange={setEmail} type="email" />
                  <Field label="Password" value={password} onChange={setPassword} type="password" />
                  <Button type="submit" disabled={loading} className="w-full">
                    {loading ? "…" : "Sign in"}
                  </Button>
                </form>
              </TabsContent>
              <TabsContent value="signup">
                <form onSubmit={signUp} className="space-y-3 mt-4">
                  <Field label="Email" value={email} onChange={setEmail} type="email" />
                  <Field label="Password" value={password} onChange={setPassword} type="password" />
                  <Button type="submit" disabled={loading} className="w-full">
                    {loading ? "…" : "Create owner account"}
                  </Button>
                  <p className="text-xs text-muted-foreground">
                    Only the first account is granted owner. Subsequent signups have no access.
                  </p>
                </form>
              </TabsContent>
            </Tabs>
          ) : (
            <form onSubmit={signIn} className="space-y-3">
              <Field label="Email" value={email} onChange={setEmail} type="email" />
              <Field label="Password" value={password} onChange={setPassword} type="password" />
              <Button type="submit" disabled={loading} className="w-full">
                {loading ? "…" : "Sign in"}
              </Button>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  type = "text",
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  type?: string;
}) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs uppercase tracking-wider text-muted-foreground">{label}</Label>
      <Input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        required
        autoComplete={type === "password" ? "current-password" : "email"}
      />
    </div>
  );
}
