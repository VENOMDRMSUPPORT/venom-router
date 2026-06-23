import { supabase } from "@/integrations/supabase/client";

async function authFetch(path: string, init?: RequestInit): Promise<Response> {
  const {
    data: { session },
  } = await supabase.auth.getSession();
  if (!session?.access_token) throw new Error("Not authenticated");

  const res = await fetch(path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${session.access_token}`,
      ...(init?.headers ?? {}),
    },
  });

  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error((body as { error?: string }).error ?? `HTTP ${res.status}`);
  }
  return res;
}

export const api = {
  get: <T>(path: string): Promise<T> => authFetch(path).then((r) => r.json() as Promise<T>),

  post: <T>(path: string, body?: unknown): Promise<T> =>
    authFetch(path, {
      method: "POST",
      body: body !== undefined ? JSON.stringify(body) : undefined,
    }).then((r) => r.json() as Promise<T>),

  patch: <T>(path: string, body?: unknown): Promise<T> =>
    authFetch(path, {
      method: "PATCH",
      body: body !== undefined ? JSON.stringify(body) : undefined,
    }).then((r) => r.json() as Promise<T>),

  delete: <T>(path: string): Promise<T> =>
    authFetch(path, { method: "DELETE" }).then((r) => r.json() as Promise<T>),
};
