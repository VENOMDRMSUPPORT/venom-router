import * as React from "react";
export interface CopyButtonProps {
    /** The text copied to the clipboard. */
    value: string;
    /** Accessible label (and tooltip) — becomes "Copied" for 1.5s after a successful copy. */
    label?: string;
    size?: "sm" | "md" | "lg";
    className?: string;
    onCopied?: () => void;
}
export declare function CopyButton(props: CopyButtonProps): React.JSX.Element;
