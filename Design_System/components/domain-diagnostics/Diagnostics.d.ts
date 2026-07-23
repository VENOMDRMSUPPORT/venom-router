import * as React from "react";
import { BadgeTone } from "../display/Badge";
export interface TraceIdProps {
    id: string;
    label?: string;
}
/** TraceId — request/attempt identifier, mono + copy. */
export declare function TraceId(props: TraceIdProps): React.JSX.Element;
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
export declare function TypedErrorDisplay(props: TypedErrorDisplayProps): React.JSX.Element;
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
export declare function DiagnosticEventRow(props: DiagnosticEventRowProps): React.JSX.Element;
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
export declare function EvidenceList(props: EvidenceListProps): React.JSX.Element;
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
export declare function AuditEventTimeline(props: AuditEventTimelineProps): React.JSX.Element;
export type JobState = "pending" | "running" | "completed" | "failed" | "expired";
export interface JobStatusProps {
    state: JobState;
    /** discovery | probe | benchmark | backup | restore, or any other job kind label. */
    kind?: string;
    jobId?: string;
    error?: React.ReactNode;
}
/** JobStatus — canonical shared job chip; kind ∈ discovery|probe|benchmark|backup|restore. */
export declare function JobStatus(props: JobStatusProps): React.JSX.Element;
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
export declare function JobProgress(props: JobProgressProps): React.JSX.Element;
export interface RawPayloadDisclosureProps {
    payload: string;
    label?: string;
    redactedFields?: string[];
}
/** RawPayloadDisclosure — collapsed sanitized payload with an explicit redaction note. Never renders secrets. */
export declare function RawPayloadDisclosure(props: RawPayloadDisclosureProps): React.JSX.Element;
