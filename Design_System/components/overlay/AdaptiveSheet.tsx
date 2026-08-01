import * as React from "react";
import { Drawer, type DrawerProps } from "./Drawer";

export type AdaptiveSheetProps = DrawerProps;

export function AdaptiveSheet(props: AdaptiveSheetProps) {
  const { className = "", ...rest } = props;
  return <Drawer {...rest} className={("vn-adaptive-sheet " + className).trim()} />;
}
