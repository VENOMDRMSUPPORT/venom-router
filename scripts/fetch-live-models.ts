/**
 * Debug script: pull Antigravity accounts from Supabase and run fetchAvailableModels
 * to see what the live API actually returns.
 *
 * Usage:  npm run test:fetch-models
 */
import { createClient } from "@supabase/supabase-js";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

// Tiny inline .env parser — avoids the dotenv dependency.
function loadEnv(file: string) {
  try {
    const text = readFileSync(file, "utf8");
    for (const line of text.split(/\r?\n/)) {
      const m = line.match(/^\s*([A-Z0-9_]+)\s*=\s*"?([^"]*)"?\s*$/i);
      if (m && !process.env[m[1]!]) process.env[m[1]!] = m[2]!;
    }
  } catch {
    /* no .env */
  }
}
loadEnv(resolve(__dirname, "../.env"));

const SUPABASE_URL = process.env.SUPABASE_URL;
const SUPABASE_KEY =
  process.env.SUPABASE_SERVICE_ROLE_KEY ??
  process.env.SUPABASE_PUBLISHABLE_KEY ??
  process.env.VITE_SUPABASE_PUBLISHABLE_KEY;

if (!SUPABASE_URL || !SUPABASE_KEY) {
  console.error("Missing SUPABASE_URL or SUPABASE key in .env");
  process.exit(1);
}

const supabase = createClient(SUPABASE_URL, SUPABASE_KEY, {
  auth: { persistSession: false, autoRefreshToken: false },
});

const ANTIGRAVITY_BASE = "https://cloudcode-pa.googleapis.com";

interface AccountRow {
  id: string;
  label: string | null;
  email: string | null;
  status: string | null;
  project_id: string | null;
  credentials_enc: unknown;
  credentials_iv: unknown;
  credentials_tag: unknown;
}

interface StoredCredentials {
  kind: "oauth2" | "api_key";
  access_token?: string;
  refresh_token?: string;
  expires_at?: number;
  project_id?: string;
}

function unpackCredentials(row: AccountRow): StoredCredentials | null {
  // The credentials are encrypted in the DB. We can't decrypt without the
  // master key, but we only need the access_token (which lives in plain memory
  // for active sessions). For debugging, we'll try to extract from raw bytes
  // by looking for the OAuth2 token JSON pattern.
  //
  // In a real test, you'd need the crypto key from the server runtime.
  // For this script we'll just print the account metadata and call the API
  // with a placeholder if we have a stored plaintext token somewhere.
  return null;
}

async function fetchAvailableModels(token: string, projectId: string): Promise<unknown> {
  const r = await fetch(`${ANTIGRAVITY_BASE}/v1internal:fetchAvailableModels`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      Accept: "application/json",
      "User-Agent": "antigravity/1.107.0 darwin/arm64",
      "X-Goog-Api-Client": "google-cloud-sdk vscode_cloudshelleditor/0.1",
      "X-Client-Name": "antigravity",
      "X-Client-Version": "1.107.0",
    },
    body: JSON.stringify({ project: projectId }),
    signal: AbortSignal.timeout(15_000),
  });
  const text = await r.text();
  if (!r.ok) throw new Error(`HTTP ${r.status}: ${text.slice(0, 200)}`);
  return JSON.parse(text);
}

async function main() {
  console.log("=== Fetch live Antigravity models for each account ===\n");

  // List providers to find Antigravity
  const { data: providers, error: provErr } = await supabase
    .from("providers")
    .select("id,slug,name");

  if (provErr) {
    console.error("Provider query failed:", provErr.message);
    console.error("This usually means RLS is blocking the read or the table is empty.");
    process.exit(1);
  }
  console.log(`Found ${providers?.length ?? 0} providers.`);

  // List all tables the key can see, to debug RLS
  console.log("\nQuick probe: trying other tables...");
  for (const tbl of ["accounts", "models", "user_settings"]) {
    const { count, error } = await supabase.from(tbl).select("*", { count: "exact", head: true });
    console.log(`  ${tbl}: count=${count} error=${error?.message ?? "none"}`);
  }

  if (!providers?.length) {
    console.log("\nNo providers visible to this key.");
    return;
  }

  console.log("All providers in DB:");
  for (const p of providers) console.log(`  - ${p.slug} (${p.name})`);
  console.log();

  // Find the Antigravity one (slug may vary)
  const antigravity = providers.find(
    (p) =>
      p.slug?.toLowerCase().includes("antigravity") ||
      p.name?.toLowerCase().includes("antigravity"),
  );
  if (!antigravity) {
    console.log("No provider matching 'antigravity' found.");
    return;
  }
  const provider = antigravity;

  for (const provider of providers) {
    console.log(`Provider: ${provider.name} (${provider.id})\n`);

    const { data: accounts, error: accErr } = await supabase
      .from("accounts")
      .select("id,label,email,status,project_id")
      .eq("provider_id", provider.id);

    if (accErr) {
      console.error("Account query failed:", accErr.message);
      continue;
    }
    if (!accounts?.length) {
      console.log("  No accounts linked to this provider.");
      continue;
    }

    for (const acc of accounts) {
      console.log(`Account: ${acc.email ?? acc.label ?? acc.id}`);
      console.log(`  status:  ${acc.status}`);
      console.log(`  project: ${acc.project_id ?? "(none)"}`);
    }
  }

  console.log("\n--- NOTE ---");
  console.log(
    "Credentials in the DB are encrypted. To call fetchAvailableModels we need a plaintext access_token.",
  );
  console.log(
    "If you have a token in your environment (e.g. ANTIGRAVITY_TEST_TOKEN) you can test that one account below.",
  );

  const testToken = process.env.ANTIGRAVITY_TEST_TOKEN;
  const testProject = process.env.ANTIGRAVITY_TEST_PROJECT;

  if (testToken && testProject) {
    console.log("\nCalling fetchAvailableModels with env-provided token...");
    try {
      const data: any = await fetchAvailableModels(testToken, testProject);
      console.log("\n=== RAW API RESPONSE ===");
      console.log(JSON.stringify(data, null, 2));

      const modelCount = data?.models ? Object.keys(data.models).length : 0;
      console.log(`\nTotal models in response: ${modelCount}`);

      if (data?.models) {
        console.log("\n=== MODEL LIST ===");
        for (const [id, entry] of Object.entries<any>(data.models)) {
          console.log(
            `  ${entry.isInternal ? "[INTERNAL] " : ""}${id} → ${entry.displayName ?? "?"}`,
          );
        }
      }

      if (data?.agentModelSorts) {
        console.log("\n=== AGENT MODEL SORTS ===");
        console.log(JSON.stringify(data.agentModelSorts, null, 2));
      }
    } catch (e: any) {
      console.error("fetch failed:", e.message);
    }
  } else {
    console.log(
      "\nSkipping live fetch (set ANTIGRAVITY_TEST_TOKEN and ANTIGRAVITY_TEST_PROJECT in env to run).",
    );
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
