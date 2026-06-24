import { AlertTriangle, CheckCircle2, Info, XCircle } from "lucide-react";
import { Toaster as Sonner } from "sonner";

type ToasterProps = React.ComponentProps<typeof Sonner>;

const Toaster = ({ ...props }: ToasterProps) => {
  return (
    <Sonner
      className="venom-toaster"
      closeButton
      expand={false}
      visibleToasts={4}
      gap={10}
      offset={20}
      icons={{
        success: <CheckCircle2 strokeWidth={2.25} />,
        error: <XCircle strokeWidth={2.25} />,
        warning: <AlertTriangle strokeWidth={2.25} />,
        info: <Info strokeWidth={2.25} />,
      }}
      toastOptions={{
        unstyled: true,
        classNames: {
          toast: "venom-toast",
          title: "venom-toast__title",
          description: "venom-toast__description",
          content: "venom-toast__content",
          icon: "venom-toast__icon",
          closeButton: "venom-toast__close",
          success: "venom-toast--success",
          error: "venom-toast--error",
          warning: "venom-toast--warning",
          info: "venom-toast--info",
          actionButton: "venom-toast__action",
          cancelButton: "venom-toast__cancel",
        },
      }}
      {...props}
    />
  );
};

export { Toaster };
