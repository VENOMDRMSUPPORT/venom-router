import { Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { toast } from "sonner";
import type { PlaygroundRequest, VenomSlug } from "./playground-types";

type Props = {
  venomSlug: VenomSlug;
  prompt: string;
  maxTokens?: number;
  temperature?: number;
};

function buildExternalBody(
  venomSlug: VenomSlug,
  prompt: string,
  maxTokens?: number,
  temperature?: number,
) {
  const body: Record<string, unknown> = {
    model: `venom/${venomSlug}`,
    messages: [{ role: "user", content: prompt }],
  };
  if (maxTokens != null) body.max_tokens = maxTokens;
  if (temperature != null) body.temperature = temperature;
  return body;
}

function origin() {
  if (typeof window !== "undefined") return window.location.origin;
  return "http://localhost:8081";
}

export function PlaygroundCopyMenu({ venomSlug, prompt, maxTokens, temperature }: Props) {
  const body = buildExternalBody(venomSlug, prompt || "Hello", maxTokens, temperature);
  const url = `${origin()}/api/v1/chat/completions`;
  const bodyJson = JSON.stringify(body, null, 2);

  const snippets: Record<string, string> = {
    cURL: `curl ${url} \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer vk_live_YOUR_KEY" \\
  -d '${JSON.stringify(body)}'`,
    JavaScript: `const res = await fetch("${url}", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    Authorization: "Bearer vk_live_YOUR_KEY",
  },
  body: JSON.stringify(${bodyJson}),
});
const data = await res.json();`,
    Python: `import requests

res = requests.post(
    "${url}",
    headers={
        "Content-Type": "application/json",
        "Authorization": "Bearer vk_live_YOUR_KEY",
    },
    json=${bodyJson.replace(/true/g, "True").replace(/false/g, "False").replace(/null/g, "None")},
)
print(res.json())`,
    "OpenAI SDK": `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "vk_live_YOUR_KEY",
  baseURL: "${origin()}/api/v1",
});

const completion = await client.chat.completions.create(${bodyJson});`,
  };

  async function copy(label: string) {
    await navigator.clipboard.writeText(snippets[label]!);
    toast.success(`Copied ${label} snippet`);
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="text-xs">
          <Copy className="h-3.5 w-3.5" /> Copy as…
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {Object.keys(snippets).map((label) => (
          <DropdownMenuItem key={label} onClick={() => copy(label)}>
            {label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
