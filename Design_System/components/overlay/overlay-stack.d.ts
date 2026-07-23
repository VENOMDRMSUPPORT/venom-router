export declare function useOverlayStack(open: boolean): {
    isTopmost: boolean;
    depth: number;
};
/** Every focusable descendant of `root`, in DOM order — the shared focus-trap query. */
export declare function getFocusable(root: HTMLElement): HTMLElement[];
