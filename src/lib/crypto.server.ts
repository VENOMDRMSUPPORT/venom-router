// AES-256-GCM helper for provider credentials. Server-only.
import { createCipheriv, createDecipheriv, randomBytes, createHash } from "crypto";

function getKey(): Buffer {
  const raw = process.env.VENOM_ENCRYPTION_KEY;
  if (!raw) {
    throw new Error("VENOM_ENCRYPTION_KEY is required. Generate one with: openssl rand -hex 32");
  }
  // Accept hex (64 chars), base64 (>=43 chars), or raw 32-byte string.
  if (/^[0-9a-f]{64}$/i.test(raw)) return Buffer.from(raw, "hex");
  const b = Buffer.from(raw, "base64");
  if (b.length === 32) return b;
  return createHash("sha256").update(raw).digest();
}

export function encryptSecret(plaintext: string): { enc: Buffer; iv: Buffer; tag: Buffer } {
  const iv = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", getKey(), iv);
  const enc = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final()]);
  const tag = cipher.getAuthTag();
  return { enc, iv, tag };
}

export function decryptSecret(enc: Buffer, iv: Buffer, tag: Buffer): string {
  const decipher = createDecipheriv("aes-256-gcm", getKey(), iv);
  decipher.setAuthTag(tag);
  return Buffer.concat([decipher.update(enc), decipher.final()]).toString("utf8");
}

export function generateApiKey(): { raw: string; prefix: string; hash: string } {
  const body = randomBytes(32).toString("base64url");
  const raw = `vk_live_${body}`;
  const prefix = raw.slice(0, 12);
  const hash = createHash("sha256").update(raw).digest("hex");
  return { raw, prefix, hash };
}

export function hashApiKey(raw: string): string {
  return createHash("sha256").update(raw).digest("hex");
}
