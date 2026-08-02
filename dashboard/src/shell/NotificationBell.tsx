import { DropdownMenu, IconButton } from "@venom/design-system/primitives";

/**
 * The header's notification bell with a dot badge matching the design images.
 */
export default function NotificationBell() {
  return (
    <DropdownMenu
      align="end"
      trigger={
        <IconButton
          icon="bell"
          label="Notifications"
          variant="ghost"
          className="relative"
        >
          <span className="absolute top-1 right-1 h-2 w-2 rounded-full bg-text-primary ring-2 ring-surface-primary" />
        </IconButton>
      }
      items={[{ label: "No notifications yet", icon: "bell", disabled: true }]}
    />
  );
}
