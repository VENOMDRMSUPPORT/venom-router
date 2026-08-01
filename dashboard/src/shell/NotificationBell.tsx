import { DropdownMenu, IconButton } from "@venom/design-system/primitives";

/**
 * The header's notification bell (legacy console affordance). There is no
 * notification system in the control plane yet, so the popover renders a
 * single disabled "No notifications yet" entry — an honest empty state,
 * never a fabricated feed. When a real notification source lands, this is
 * the mount point.
 */
export default function NotificationBell() {
  return (
    <DropdownMenu
      align="end"
      trigger={<IconButton icon="bell" label="Notifications" variant="ghost" />}
      items={[{ label: "No notifications yet", icon: "bell", disabled: true }]}
    />
  );
}
