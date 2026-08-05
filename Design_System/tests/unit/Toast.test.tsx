import { test, expect } from "@playwright/test";
import { toast, toastManager } from "../../components/feedback/ToastContext";

test.describe("Toast System", () => {
  test("adds and dismisses toasts via imperative API", async () => {
    toast.clearAll();
    
    const id = toast.success("Test Success Notification", { detail: "Detailed description" });
    expect(id).toBeDefined();

    toast.danger("Test Error", { detail: "Error details" });
    toast.warning("Test Warning");
    toast.info("Test Info");

    const toastId = toast.loading("Loading process...");
    expect(toastId).toBeDefined();

    toast.dismiss(id);
    toast.clearAll();
  });

  test("handles promise toasts correctly", async () => {
    toast.clearAll();

    const successPromise = Promise.resolve("done");
    const result = await toast.promise(successPromise, {
      loading: "Processing...",
      success: "Operation Completed",
      error: "Operation Failed",
    });

    expect(result).toBe("done");
  });
});
