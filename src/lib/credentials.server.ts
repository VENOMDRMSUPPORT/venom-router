/* Encrypt/decrypt the credentials JSON blob stored on accounts. Server-only. */
import { encryptSecret, decryptSecret } from "@/lib/crypto.server";
import type { StoredCredentials } from "@/lib/providers/adapters/types";

/** PostgREST bytea wire format — raw Buffer JSON breaks round-trip through Supabase. */
function toPgBytea(buf: Buffer): string {
  return `\\x${buf.toString("hex")}`;
}

export function packCredentials(creds: StoredCredentials): {
  credentials_enc: string;
  credentials_iv: string;
  credentials_tag: string;
} {
  const { enc, iv, tag } = encryptSecret(JSON.stringify(creds));
  return {
    credentials_enc: toPgBytea(enc),
    credentials_iv: toPgBytea(iv),
    credentials_tag: toPgBytea(tag),
  };
}

export function unpackCredentials(row: {
  credentials_enc: Buffer | Uint8Array | string | { type: string; data: number[] } | null;
  credentials_iv: Buffer | Uint8Array | string | { type: string; data: number[] } | null;
  credentials_tag: Buffer | Uint8Array | string | { type: string; data: number[] } | null;
}): StoredCredentials {
  const enc = toBuf(row.credentials_enc, "credentials_enc");
  const iv = toBuf(row.credentials_iv, "credentials_iv");
  const tag = toBuf(row.credentials_tag, "credentials_tag");
  const json = decryptSecret(enc, iv, tag);
  return JSON.parse(json) as StoredCredentials;
}

function toBuf(
  v: Buffer | Uint8Array | string | { type: string; data: number[] } | null | undefined,
  field = "bytea",
): Buffer {
  if (v == null) throw new Error(`Missing ${field}`);
  if (Buffer.isBuffer(v)) return v;
  if (typeof v === "object" && "type" in v && v.type === "Buffer" && Array.isArray(v.data)) {
    return Buffer.from(v.data);
  }
  if (typeof v === "string") {
    if (v.startsWith("\\x") || v.startsWith("\\X")) return Buffer.from(v.slice(2), "hex");
    if (/^[0-9a-f]+$/i.test(v) && v.length % 2 === 0) return Buffer.from(v, "hex");
    const b64 = Buffer.from(v, "base64");
    if (b64.length > 0) return b64;
    throw new Error(`Unrecognized ${field} encoding`);
  }
  return Buffer.from(v);
}
