import * as React from "react";
export interface CodeBlockProps {
    code: string;
    label?: string;
    copyable?: boolean;
    className?: string;
}
export declare function CodeBlock(props: CodeBlockProps): React.JSX.Element;
export interface CodeProps {
    children?: React.ReactNode;
    className?: string;
}
/** Code — inline code span. */
export declare function Code(props: CodeProps): React.JSX.Element;
