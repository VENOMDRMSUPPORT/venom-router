import * as React from "react";
export interface TagProps {
    onRemove?: () => void;
    removeLabel?: string;
    icon?: string;
    mono?: boolean;
    children?: React.ReactNode;
    className?: string;
}
export declare function Tag(props: TagProps): React.JSX.Element;
