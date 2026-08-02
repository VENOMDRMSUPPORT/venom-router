import { useSyncExternalStore } from "react";
import { Drawer, IconButton } from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import { debugLog, type DebugLogEvent } from "../api/controlClient";
import "../fleet/fleet.css";

export interface DebugLogPanelProps {
  open: boolean;
  onClose: () => void;
}

/** hh:mm:ss.mmm for the mono event line. */
function formatTime(atMs: number): string {
  const d = new Date(atMs);
  const pad = (n: number, w = 2) => String(n).padStart(w, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
}

function EventLine(props: { event: DebugLogEvent }) {
  const e = props.event;
  return (
    <div className="vnd-debug-row">
      <span className="vnd-debug-time">{formatTime(e.at)}</span>
      <span className="vnd-debug-path">
        {e.method} {e.path}
      </span>
      <span className={e.ok ? "vnd-debug-status--ok" : "vnd-debug-status--fail"}>→ {e.status}</span>
      <span className="vnd-debug-duration">{e.durationMs} ms</span>
      {e.requestId ? <span className="vnd-debug-duration">req {e.requestId}</span> : null}
    </div>
  );
}

/**
 * The Debug Log side panel (image 11): every control-API exchange the page
 * performed — method, path, status, duration, request id — newest first,
 * from controlClient's secret-free ring buffer (no bodies, no headers, no
 * query strings, by construction). "Clear log" empties the buffer and is
 * disabled while it already is; × closes the panel.
 */
export default function DebugLogPanel(props: DebugLogPanelProps) {
  const { open, onClose } = props;

  const events = useSyncExternalStore(debugLog.subscribe, debugLog.snapshot);

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={
        <span className="flex items-center gap-2">
          <Icon name="terminal" size={15} />
          Debug Log
        </span>
      }
    >
      <div className="vnd-debug-toolbar">
        <IconButton
          icon="trash-2"
          label="Clear log"
          title="Clear log"
          variant="ghost"
          size="sm"
          disabled={events.length === 0}
          onClick={() => debugLog.clear()}
        />
      </div>
      {events.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-8 text-center">
          <span className="vnd-debug-empty-icon">
            <Icon name="clock" size={18} />
          </span>
          <span className="vn-body-compact font-semibold">No operations captured</span>
          <span className="vn-caption">
            Open Debug from a page and perform an action to capture request/response events here.
          </span>
        </div>
      ) : (
        <div className="vnd-debug-list" aria-label="Captured operations" role="list">
          {events
            .slice()
            .reverse()
            .map((event) => (
              <div role="listitem" key={event.id}>
                <EventLine event={event} />
              </div>
            ))}
        </div>
      )}
    </Drawer>
  );
}
