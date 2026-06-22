import { Toaster as Sonner } from "sonner";

type ToasterProps = React.ComponentProps<typeof Sonner>;

const Toaster = ({ ...props }: ToasterProps) => {
  return (
    <Sonner
      className="toaster group"
      toastOptions={{
        classNames: {
          toast:
            "group toast group-[.toaster]:bg-card/90 group-[.toaster]:text-foreground group-[.toaster]:border-border/50 group-[.toaster]:shadow-elegant group-[.toaster]:backdrop-blur-md group-[.toast]:rounded-xl group-[.toast]:border group-[.toast]:font-sans group-[.toast]:transition-all group-[.toast]:duration-300",
          title:
            "group-[.toast]:font-display group-[.toast]:font-semibold group-[.toast]:text-sm group-[.toast]:tracking-tight",
          description:
            "group-[.toast]:font-sans group-[.toast]:text-muted-foreground group-[.toast]:text-xs",
          success:
            "group-[.toast]:bg-success/5 dark:group-[.toast]:bg-success/10 group-[.toast]:text-success group-[.toast]:border-success/30 group-[.toast]:shadow-[0_0_20px_-4px_var(--success)]",
          error:
            "group-[.toast]:bg-destructive/5 dark:group-[.toast]:bg-destructive/10 group-[.toast]:text-destructive group-[.toast]:border-destructive/30 group-[.toast]:shadow-[0_0_20px_-4px_var(--destructive)]",
          warning:
            "group-[.toast]:bg-warning/5 dark:group-[.toast]:bg-warning/10 group-[.toast]:text-warning group-[.toast]:border-warning/30 group-[.toast]:shadow-[0_0_20px_-4px_var(--warning)]",
          info:
            "group-[.toast]:bg-primary/5 dark:group-[.toast]:bg-primary/10 group-[.toast]:text-primary group-[.toast]:border-primary/30 group-[.toast]:shadow-[0_0_20px_-4px_var(--primary)]",
          actionButton:
            "group-[.toast]:bg-primary group-[.toast]:text-primary-foreground group-[.toast]:font-medium group-[.toast]:rounded-md",
          cancelButton:
            "group-[.toast]:bg-muted group-[.toast]:text-muted-foreground group-[.toast]:font-medium group-[.toast]:rounded-md",
        },
      }}
      {...props}
    />
  );
};

export { Toaster };
