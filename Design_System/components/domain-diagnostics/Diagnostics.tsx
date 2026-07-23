import * as React from "react";
import { Icon } from "../icons/Icon";
import { Badge, BadgeTone } from "../display/Badge";
import { CopyButton } from "../actions/CopyButton";

/* Diagnostics set (docs/05 §7, 09). Records carry ids/codes/scores only —
   components assume sanitized inputs and add redaction affordances on top. */

export interface TraceIdProps {
  id: string;
  label?: string;
}

/** TraceId — request/attempt identifier, mono + copy. */
export function TraceId(props: TraceIdProps) {
  const { id, label = "trace id" } = props;
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 2 }}>
      <span className="vn-code-inline">{id}</span>
      <CopyButton value={id} label={"Copy " + label} />
    </span>
  );
}

export type TypedErrorTone = "critical" | "warning" | "info";

export interface TypedErrorDisplayProps {
  /** Stable, typed error code — rendered verbatim in mono. */
  code: string;
  message?: React.ReactNode;
  requestId?: string;
  retryable?: boolean;
  retryAfter?: React.ReactNode;
  tone?: TypedErrorTone;
}

/** TypedErrorDisplay — the stable error envelope: code (mono) + user-safe message + retryable. Never raw provider text. */
export function TypedErrorDisplay(props: TypedErrorDisplayProps) {
  const { code, message, requestId, retryable, retryAfter, tone = "critical" } = props;
  return (
    <div className={"vn-alert vn-alert--" + tone} role={tone === "critical" ? "alert" : "status"}>
      <Icon name={tone === "critical" ? "circle-x" : "triangle-alert"} size={15} />
      <div style={{ flex: 1 }}>
        <p className="vn-alert-title" style={{ display: "flex", gap: "var(--space-2)", alignItems: "center", flexWrap: "wrap" }}>
          <span className="vn-reason-code vn-reason-code--blocking">{code}</span>
          {retryable != null ? <Badge tone={retryable ? "info" : "inactive"} icon={retryable ? "refresh-cw" : "ban"}>{retryable ? "retryable" : "not retryable"}</Badge> : null}
          {retryAfter ? <span className="vn-caption vn-mono-xs">retry after {retryAfter}</span> : null}
        </p>
        <div>{message}</div>
        {requestId ? <div className="vn-alert-code" style={{ marginTop: 4 }}>request_id: {requestId}</div> : null}
      </div>
    </div>
  );
}

export interface DiagnosticEventRowProps {
  time?: React.ReactNode;
  kind?: React.ReactNode;
  scope?: React.ReactNode;
  code?: React.ReactNode;
  detail?: React.ReactNode;
  latency?: React.ReactNode;
  tone?: BadgeTone;
}

/** DiagnosticEventRow — one normalized event: time · kind · scope · code · latency. */
export function DiagnosticEventRow(props: DiagnosticEventRowProps) {
  const { time, kind, scope, code, detail, latency, tone = "inactive" } = props;
  return (
    <div className="vn-evidence-row" style={{ gridTemplateColumns: "max-content max-content 1fr max-content" }}>
      <span className="vn-mono-xs vn-text-muted">{time}</span>
      <Badge tone={tone} mono>{kind}</Badge>
      <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
        <span style={{ display: "flex", gap: "var(--space-1)", flexWrap: "wrap", alignItems: "center" }}>
          {scope ? <span className="vn-reason-code"><Icon name="route" size={11} />{scope}</span> : null}
          {code ? <span className="vn-reason-code vn-reason-code--blocking">{code}</span> : null}
        </span>
        {detail ? <span className="vn-caption">{detail}</span> : null}
      </span>
      <span className="vn-data vn-text-muted">{latency || ""}</span>
    </div>
  );
}

export interface EvidenceItem {
  field: React.ReactNode;
  value?: React.ReactNode;
  mono?: boolean;
  source?: React.ReactNode;
  confidence?: number;
  exactMatch?: boolean;
  observedAt?: React.ReactNode;
}

export interface EvidenceListProps {
  items?: EvidenceItem[];
}

/** EvidenceList — provenance-wrapped facts: value + source + confidence + observed/expiry + identity match. */
export function EvidenceList(props: EvidenceListProps) {
  const { items = [] } = props;
  return (
    <div className="vn-evidence">
      {items.map((it, i) => (
        <div className="vn-evidence-row" key={i}>
          <span className="vn-label">{it.field}</span>
          <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
            <span className={it.mono ? "vn-mono" : "vn-body-compact"}>{it.value == null ? <Badge tone="unknown" icon="circle-help">unknown</Badge> : it.value}</span>
            <span className="vn-caption" style={{ display: "flex", gap: "var(--space-2)", alignItems: "center", flexWrap: "wrap" }}>
              {it.source ? <span className="vn-reason-code">{it.source}</span> : null}
              {it.confidence != null ? "confidence " + it.confidence : null}
              {it.exactMatch === false ? <Badge tone="warning" icon="triangle-alert">family match</Badge> : null}
              {it.observedAt ? <span className="vn-mono-xs">{it.observedAt}</span> : null}
            </span>
          </span>
          <span></span>
        </div>
      ))}
    </div>
  );
}

export interface AuditEvent {
  action: React.ReactNode;
  summary?: React.ReactNode;
  at?: React.ReactNode;
  actor?: React.ReactNode;
  tone?: "healthy" | "critical" | "warning" | "accent";
}

export interface AuditEventTimelineProps {
  events?: AuditEvent[];
}

/** AuditEventTimeline — append-only audit trail (ids/codes/timestamps only). */
export function AuditEventTimeline(props: AuditEventTimelineProps) {
  const { events = [] } = props;
  return (
    <ol className="vn-timeline">
      {events.map((e, i) => (
        <li key={i}>
          <span className={"vn-timeline-dot" + (e.tone ? " vn-timeline-dot--" + e.tone : "")} aria-hidden="true"></span>
          <div className="vn-body-compact" style={{ display: "flex", gap: "var(--space-2)", alignItems: "center", flexWrap: "wrap" }}>
            <span className="vn-reason-code">{e.action}</span>{e.summary}
          </div>
          <div className="vn-caption vn-mono-xs">{e.at}{e.actor ? " · " + e.actor : ""}</div>
        </li>
      ))}
    </ol>
  );
}

/* Shared async jobs (docs/09 §3.12): pending | running | completed | failed | expired. */
export type JobState = "pending" | "running" | "completed" | "failed" | "expired";

const JOB: Record<JobState, { tone: BadgeTone; icon: string; label: string }> = {
  pending:   { tone: "inactive", icon: "clock",         label: "Pending" },
  running:   { tone: "info",     icon: "loader-circle", label: "Running" },
  completed: { tone: "healthy",  icon: "circle-check",  label: "Completed" },
  failed:    { tone: "critical", icon: "circle-x",      label: "Failed" },
  expired:   { tone: "warning",  icon: "hourglass",     label: "Expired" },
};

export interface JobStatusProps {
  state: JobState;
  /** discovery | probe | benchmark | backup | restore, or any other job kind label. */
  kind?: string;
  jobId?: string;
  error?: React.ReactNode;
}

/** JobStatus — canonical shared job chip; kind ∈ discovery|probe|benchmark|backup|restore. */
export function JobStatus(props: JobStatusProps) {
  const { state, kind, jobId, error } = props;
  const m = JOB[state] || JOB.pending;
  return (
    <span style={{ display: "inline-flex", gap: "var(--space-2)", alignItems: "center", flexWrap: "wrap" }}>
      <Badge tone={m.tone} icon={m.icon} title={"job status: " + state + (jobId ? " · " + jobId : "")}>{kind ? kind + " · " : ""}{m.label}</Badge>
      {jobId ? <span className="vn-mono-xs vn-text-muted">{jobId}</span> : null}
      {error ? <span className="vn-reason-code vn-reason-code--blocking">{error}</span> : null}
    </span>
  );
}

export interface JobProgressProps {
  state: JobState;
  kind?: string;
  jobId?: string;
  /** 0-100. Omit for an indeterminate bar. */
  progress?: number;
  detail?: React.ReactNode;
  error?: React.ReactNode;
}

/** JobProgress — a polled job with progress + live-region announcement. */
export function JobProgress(props: JobProgressProps) {
  const { state, kind, jobId, progress, detail, error } = props;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-1)" }} aria-live="polite">
      <JobStatus state={state} kind={kind} jobId={jobId} error={error} />
      {state === "running" ? (
        <div className={"vn-progress" + (progress == null ? " vn-progress--indeterminate" : "")} role="progressbar" aria-label={(kind ? kind + " " : "") + "progress"} aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress ?? undefined}>
          <div style={{ width: (progress ?? 40) + "%" }}></div>
        </div>
      ) : null}
      {detail ? <span className="vn-caption">{detail}</span> : null}
    </div>
  );
}

export interface RawPayloadDisclosureProps {
  payload: string;
  label?: string;
  redactedFields?: string[];
}

/** RawPayloadDisclosure — collapsed sanitized payload with an explicit redaction note. Never renders secrets. */
export function RawPayloadDisclosure(props: RawPayloadDisclosureProps) {
  const { payload, label = "Raw evidence", redactedFields = [] } = props;
  return (
    <details className="vn-payload">
      <summary>
        <Icon name="chevron-right" size={12} />
        {label}
        <span className="vn-redaction-note" style={{ marginLeft: "auto" }}>
          <Icon name="shield" size={11} />
          sanitized · credentials fully redacted{redactedFields.length ? " (" + redactedFields.join(", ") + ")" : ""}
        </span>
      </summary>
      <pre className="vn-codeblock vn-scroll" style={{ maxHeight: 240 }}><code>{payload}</code></pre>
    </details>
  );
}
