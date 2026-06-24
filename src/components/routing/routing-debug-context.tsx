import { createContext } from "react";

export type RoutingDebugController = {
  start: (op: string, req: unknown) => string;
  resolve: (id: string, res: unknown, ms: number) => void;
  reject: (id: string, err: string, ms: number) => void;
};

export const RoutingDebugContext = createContext<RoutingDebugController | null>(null);
