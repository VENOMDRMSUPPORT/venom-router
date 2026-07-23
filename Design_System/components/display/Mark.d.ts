import * as React from "react";
export interface MarkProps {
    /** Name/slug used to derive deterministic initials when no `src` image is given. */
    name: string;
    src?: string;
    size?: "sm" | "md" | "lg";
    label?: string;
    className?: string;
}
export declare function Mark(props: MarkProps): React.JSX.Element;
