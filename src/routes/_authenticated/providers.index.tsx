import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/_authenticated/providers/")({
  beforeLoad: () => {
    throw redirect({ to: "/providers/oauth" });
  },
});
