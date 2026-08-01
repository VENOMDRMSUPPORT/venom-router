import * as React from "react";
export interface SectionDeckItem {
    key: string;
    label: string;
    icon: string;
}
export interface SectionDeckSection {
    key: string;
    label: string;
    icon: string;
    items: readonly SectionDeckItem[];
}
export interface SectionDeckProps {
    sections: readonly SectionDeckSection[];
    activeKey: string;
    onNavigate: (key: string) => void;
    label?: string;
    className?: string;
}
export declare function SectionDeck(props: SectionDeckProps): React.JSX.Element;
