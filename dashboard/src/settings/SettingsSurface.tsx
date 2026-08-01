import { useCallback, useEffect, useState } from "react";
import {
  Badge,
  Button,
  Card,
  ErrorState,
  FormField,
  Input,
  KeyValueList,
  Select,
  Spinner,
  Switch,
} from "@venom/design-system/primitives";
import { DestructiveActionConfirmation } from "@venom/design-system/domain";
import { THEMES, THEME_LABELS } from "@venom/design-system/themes";
import { ACCENTS, ACCENT_LABELS } from "@venom/design-system/customizer";
import {
  getFullSettings,
  isSessionExpired,
  putFullSettings,
  toApiError,
  type AuthApiError,
  type FullSettings,
  type SettingsUpdate,
} from "../api/controlClient";
import { applyAppearanceSettings, type AccentName, type DensityName, type ThemeName } from "../theme-runtime";

export interface SettingsSurfaceProps {
  csrfToken: string;
  onSessionExpired: () => void;
}

/**
 * The density vocabulary, taken from the server's own validation message
 * ("density must be one of comfortable, compact").
 *
 * Unlike themes and accents, the design system ships no DENSITIES array to read,
 * so this is the one vocabulary that cannot be derived from the package. It is
 * kept beside the others with that stated, rather than silently blended in.
 */
const DENSITIES: readonly DensityName[] = ["comfortable", "compact"] as const;

/** The operational fields, keyed exactly as the PUT contract names them. */
type OperationalKey =
  | "enrichment_enabled"
  | "quota_staleness_seconds"
  | "probe_max_in_flight_per_provider"
  | "probe_expensive_enabled"
  | "probe_per_account_window_seconds";

/** Every field name the PUT body can carry, used to attribute an API validation
 * message to the field it names. */
const PUT_FIELD_NAMES = [
  "theme",
  "density",
  "accent",
  "radius_px",
  "spacing_scale",
  "enrichment_enabled",
  "quota_staleness_seconds",
  "probe_max_in_flight_per_provider",
  "probe_expensive_enabled",
  "probe_per_account_window_seconds",
] as const;

/**
 * Attributes an API validation message to the field it names.
 *
 * The server's messages are field-first — "radius_px must be an integer between 0
 * and 16", "theme must be one of ..." — so the leading token identifies the field.
 * When no known field is named (e.g. the generic "invalid request body"), this
 * returns null and the caller shows a form-level error instead. Pinning an
 * unattributable message to an arbitrary field would send the owner to fix the
 * wrong input.
 */
function fieldForValidationMessage(message: string): string | null {
  const head = message.trim().split(/[\s:]/)[0];
  return PUT_FIELD_NAMES.includes(head as (typeof PUT_FIELD_NAMES)[number]) ? head : null;
}

/** The form's own bounds, mirroring the server's. The API remains the authority —
 * these only spare the owner a round trip, and every one of them is re-checked
 * server-side. */
const BOUNDS: Record<string, { min: number; max: number; integer: boolean; label: string }> = {
  radius_px: { min: 0, max: 16, integer: true, label: "an integer between 0 and 16" },
  spacing_scale: { min: 0.75, max: 1.25, integer: false, label: "a number between 0.75 and 1.25" },
  quota_staleness_seconds: { min: 1, max: 86_400, integer: true, label: "an integer between 1 and 86400" },
  probe_max_in_flight_per_provider: { min: 1, max: 100, integer: true, label: "an integer between 1 and 100" },
  probe_per_account_window_seconds: { min: 1, max: 86_400, integer: true, label: "an integer between 1 and 86400" },
};

/** Validates one numeric field against its bound, returning a message or null. */
function boundError(key: string, raw: string): string | null {
  const bound = BOUNDS[key];
  if (!bound) return null;
  const value = Number(raw);
  if (raw.trim() === "" || !Number.isFinite(value)) return `${key} must be ${bound.label}`;
  if (bound.integer && !Number.isInteger(value)) return `${key} must be ${bound.label}`;
  if (value < bound.min || value > bound.max) return `${key} must be ${bound.label}`;
  return null;
}

/** The editable form state. Numerics are held as STRINGS so a half-typed value is
 * never coerced to a number mid-edit. */
interface FormState {
  theme: ThemeName;
  density: DensityName;
  accent: AccentName;
  radius_px: string;
  spacing_scale: string;
  enrichment_enabled: boolean;
  quota_staleness_seconds: string;
  probe_max_in_flight_per_provider: string;
  probe_expensive_enabled: boolean;
  probe_per_account_window_seconds: string;
}

function formFromSettings(s: FullSettings): FormState {
  return {
    theme: s.theme,
    density: s.density,
    accent: s.accent,
    radius_px: String(s.radius_px),
    spacing_scale: String(s.spacing_scale),
    enrichment_enabled: s.enrichment_enabled,
    quota_staleness_seconds: String(s.quota_staleness_seconds),
    probe_max_in_flight_per_provider: String(s.probe_max_in_flight_per_provider),
    probe_expensive_enabled: s.probe_expensive_enabled,
    probe_per_account_window_seconds: String(s.probe_per_account_window_seconds),
  };
}

/**
 * The Settings surface (P6-UI-010, 07 §6, 09 §2).
 *
 * TWO CONTRACT RULES SHAPE THIS WHOLE COMPONENT.
 *
 * 1. The PUT's operational fields are OPTIONAL POINTERS server-side, and absent
 *    means UNCHANGED — never "reset to the default". So the body carries only the
 *    operational fields the owner actually touched, tracked in `touched`. Sending
 *    an untouched field back at its current value would turn every save into a
 *    write across settings the owner never opened, and sending it as an explicit
 *    null is a 400. The five appearance fields ARE required and always sent.
 *
 * 2. `effective_config` (the binds) is READ-ONLY. It is displayed and never
 *    submitted — it is not in SettingsUpdate's type at all, so it cannot be sent
 *    by accident.
 *
 * The theme and accent vocabularies are read from the design-system package
 * (THEMES, ACCENTS) rather than written down here: they are being changed
 * elsewhere, and a hardcoded list would either offer a value the server rejects or
 * hide one it accepts — silently, either way.
 */
export default function SettingsSurface(props: SettingsSurfaceProps) {
  const { csrfToken, onSessionExpired } = props;

  const [loaded, setLoaded] = useState<FullSettings | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [form, setForm] = useState<FormState | null>(null);
  // WHICH operational fields the owner touched. The PUT body is built from this,
  // not from a diff against the loaded values — a field explicitly set back to its
  // original value is still an intentional write, and one never opened is not.
  const [touched, setTouched] = useState<Set<OperationalKey>>(new Set());
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<AuthApiError | null>(null);
  const [saving, setSaving] = useState(false);
  const [savedNote, setSavedNote] = useState<string | null>(null);
  const [pendingConfirm, setPendingConfirm] = useState<SettingsUpdate | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const s = await getFullSettings();
        if (cancelled) return;
        setLoaded(s);
        setForm(formFromSettings(s));
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setLoadError(toApiError(err));
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, [onSessionExpired]);

  const setField = useCallback(<K extends keyof FormState>(key: K, value: FormState[K], operational?: OperationalKey) => {
    setForm((prev) => (prev ? { ...prev, [key]: value } : prev));
    setSavedNote(null);
    setFieldErrors((prev) => {
      const next = { ...prev };
      delete next[key as string];
      return next;
    });
    if (operational) setTouched((prev) => new Set(prev).add(operational));
  }, []);

  /** Builds the PUT body: the required appearance five, plus ONLY the touched
   * operational fields. Returns null when the form has a bound violation. */
  const buildUpdate = useCallback((): SettingsUpdate | null => {
    if (!form) return null;

    const errors: Record<string, string> = {};
    for (const key of ["radius_px", "spacing_scale", "quota_staleness_seconds", "probe_max_in_flight_per_provider", "probe_per_account_window_seconds"]) {
      // Only validate a numeric the owner can have broken: the appearance two are
      // always sent, the operational ones only when touched.
      const isOperational = key !== "radius_px" && key !== "spacing_scale";
      if (isOperational && !touched.has(key as OperationalKey)) continue;
      const err = boundError(key, form[key as keyof FormState] as string);
      if (err) errors[key] = err;
    }
    if (Object.keys(errors).length > 0) {
      setFieldErrors(errors);
      return null;
    }

    const update: SettingsUpdate = {
      theme: form.theme,
      density: form.density,
      accent: form.accent,
      radius_px: Number(form.radius_px),
      spacing_scale: Number(form.spacing_scale),
    };
    // Each of these is added ONLY when touched. An omitted key means "unchanged"
    // to the server; a null would be a 400.
    if (touched.has("enrichment_enabled")) update.enrichment_enabled = form.enrichment_enabled;
    if (touched.has("probe_expensive_enabled")) update.probe_expensive_enabled = form.probe_expensive_enabled;
    if (touched.has("quota_staleness_seconds")) update.quota_staleness_seconds = Number(form.quota_staleness_seconds);
    if (touched.has("probe_max_in_flight_per_provider")) {
      update.probe_max_in_flight_per_provider = Number(form.probe_max_in_flight_per_provider);
    }
    if (touched.has("probe_per_account_window_seconds")) {
      update.probe_per_account_window_seconds = Number(form.probe_per_account_window_seconds);
    }
    return update;
  }, [form, touched]);

  const submit = useCallback(
    async (update: SettingsUpdate) => {
      setSaving(true);
      setFormError(null);
      setFieldErrors({});
      try {
        const saved = await putFullSettings(update, csrfToken);
        setLoaded(saved);
        setForm(formFromSettings(saved));
        setTouched(new Set());
        setSavedNote("Settings saved.");
        // Appearance takes effect immediately, through the DS apply* functions —
        // never hand-rolled attribute writes.
        applyAppearanceSettings({
          theme: saved.theme,
          density: saved.density,
          accent: saved.accent,
          radius_px: saved.radius_px,
          spacing_scale: saved.spacing_scale,
        });
      } catch (err) {
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        const apiError = toApiError(err);
        // The API's answer is the truth. Attribute it to the field it names, and
        // fall back to a form-level error only when it names none — pinning an
        // unattributable message to a field would send the owner to the wrong input.
        const field = apiError.code === "validation_error" ? fieldForValidationMessage(apiError.message) : null;
        if (field) {
          setFieldErrors({ [field]: apiError.message });
        } else {
          setFormError(apiError);
        }
      } finally {
        setSaving(false);
      }
    },
    [csrfToken, onSessionExpired],
  );

  const handleSave = useCallback(() => {
    const update = buildUpdate();
    if (!update) return;
    // Enabling enrichment starts outbound calls to an external metadata source
    // (04 §2b) — not a silent appearance tweak, so it is confirmed before the write.
    if (update.enrichment_enabled === true) {
      setPendingConfirm(update);
      return;
    }
    void submit(update);
  }, [buildUpdate, submit]);

  if (loadError) {
    // Deliberately NOT an empty form: an empty form invites the owner to submit
    // defaults over the real configuration they could not read.
    return (
      <ErrorState
        code={loadError.code}
        title="Could not load settings"
        description={loadError.message}
      />
    );
  }

  if (form === null || loaded === null) {
    return <Spinner label="Loading settings…" />;
  }

  const fieldError = (key: string) => fieldErrors[key];

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-col gap-3">
          <h3 className="vn-h3">Appearance</h3>
          <FormField label="Theme" error={fieldError("theme")} required>
            <Select value={form.theme} onChange={(e) => setField("theme", e.target.value as ThemeName)}>
              {/* Straight from the design-system package — never a literal list. */}
              {THEMES.map((t) => (
                <option key={t} value={t}>
                  {THEME_LABELS[t] ?? t}
                </option>
              ))}
            </Select>
          </FormField>
          {fieldError("theme") ? <span data-testid="field-error-theme" className="vn-caption">{fieldError("theme")}</span> : null}

          <FormField label="Density" error={fieldError("density")} required>
            <Select value={form.density} onChange={(e) => setField("density", e.target.value as DensityName)}>
              {DENSITIES.map((d) => (
                <option key={d} value={d}>
                  {d}
                </option>
              ))}
            </Select>
          </FormField>

          <FormField label="Accent" error={fieldError("accent")} required>
            <Select value={form.accent} onChange={(e) => setField("accent", e.target.value as AccentName)}>
              {ACCENTS.map((a) => (
                <option key={a} value={a}>
                  {ACCENT_LABELS[a] ?? a}
                </option>
              ))}
            </Select>
          </FormField>

          <FormField label="Corner radius" description="0 to 16." error={fieldError("radius_px")} required>
            <Input
              inputMode="numeric"
              value={form.radius_px}
              onChange={(e) => setField("radius_px", e.target.value)}
            />
          </FormField>
          {fieldError("radius_px") ? (
            <span data-testid="field-error-radius_px" className="vn-caption">
              {fieldError("radius_px")}
            </span>
          ) : null}

          <FormField label="Spacing scale" description="0.75 to 1.25." error={fieldError("spacing_scale")} required>
            <Input
              inputMode="decimal"
              value={form.spacing_scale}
              onChange={(e) => setField("spacing_scale", e.target.value)}
            />
          </FormField>
          {fieldError("spacing_scale") ? (
            <span data-testid="field-error-spacing_scale" className="vn-caption">
              {fieldError("spacing_scale")}
            </span>
          ) : null}
        </div>
      </Card>

      <Card>
        <div className="flex flex-col gap-3">
          <h3 className="vn-h3">Operational</h3>
          <span className="vn-caption">
            A setting you do not touch is left out of the save entirely, so it keeps its current
            value rather than being reset.
          </span>

          <FormField
            label="Quota staleness (seconds)"
            description="How old provider quota evidence may be before it is treated as unknown."
            error={fieldError("quota_staleness_seconds")}
          >
            <Input
              inputMode="numeric"
              value={form.quota_staleness_seconds}
              onChange={(e) => setField("quota_staleness_seconds", e.target.value, "quota_staleness_seconds")}
            />
          </FormField>
          {fieldError("quota_staleness_seconds") ? (
            <span data-testid="field-error-quota_staleness_seconds" className="vn-caption">
              {fieldError("quota_staleness_seconds")}
            </span>
          ) : null}

          <FormField
            label="Probe max in flight per provider"
            error={fieldError("probe_max_in_flight_per_provider")}
          >
            <Input
              inputMode="numeric"
              value={form.probe_max_in_flight_per_provider}
              onChange={(e) =>
                setField("probe_max_in_flight_per_provider", e.target.value, "probe_max_in_flight_per_provider")
              }
            />
          </FormField>
          {fieldError("probe_max_in_flight_per_provider") ? (
            <span data-testid="field-error-probe_max_in_flight_per_provider" className="vn-caption">
              {fieldError("probe_max_in_flight_per_provider")}
            </span>
          ) : null}

          <FormField
            label="Probe per-account window (seconds)"
            error={fieldError("probe_per_account_window_seconds")}
          >
            <Input
              inputMode="numeric"
              value={form.probe_per_account_window_seconds}
              onChange={(e) =>
                setField("probe_per_account_window_seconds", e.target.value, "probe_per_account_window_seconds")
              }
            />
          </FormField>
          {fieldError("probe_per_account_window_seconds") ? (
            <span data-testid="field-error-probe_per_account_window_seconds" className="vn-caption">
              {fieldError("probe_per_account_window_seconds")}
            </span>
          ) : null}

          <Switch
            label="Expensive probes"
            checked={form.probe_expensive_enabled}
            onChange={(e) => setField("probe_expensive_enabled", e.target.checked, "probe_expensive_enabled")}
          />

          <Switch
            label="Enrichment — allows outbound calls to an external model-metadata source"
            checked={form.enrichment_enabled}
            onChange={(e) => setField("enrichment_enabled", e.target.checked, "enrichment_enabled")}
          />
        </div>
      </Card>

      {/* READ-ONLY. effective_config is not part of SettingsUpdate's type, so it
          cannot reach a PUT body by accident. */}
      <Card data-testid="effective-config">
        <div className="flex flex-col gap-2">
          <h3 className="vn-h3">Effective configuration</h3>
          <span className="vn-caption">
            Read-only. These are resolved at boot from the command line and environment, so they
            change only on restart — there is no endpoint that sets them.
          </span>
          <KeyValueList
            items={[
              { key: "Control bind", value: loaded.effective_config.bind, mono: true },
              {
                key: "Data-plane bind",
                value:
                  loaded.effective_config.data_plane_bind === null
                    ? "not configured — the public /v1 API shares the control listener"
                    : loaded.effective_config.data_plane_bind,
                mono: true,
              },
            ]}
          />
        </div>
      </Card>

      {formError ? (
        <div data-testid="settings-form-error">
          <ErrorState
            variant="inline"
            code={formError.code}
            title="Could not save settings"
            description={formError.message}
          />
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-3">
        <Button variant="primary" onClick={handleSave} disabled={saving}>
          Save settings
        </Button>
        {savedNote ? (
          <Badge tone="healthy" icon="circle-check">
            {savedNote}
          </Badge>
        ) : null}
      </div>

      <DestructiveActionConfirmation
        open={pendingConfirm !== null}
        title="Enable enrichment?"
        consequence="Enrichment allows outbound requests to an external model-metadata source. No provider credential is sent, but the router will start making network calls it does not make today."
        confirmLabel="Apply"
        onConfirm={() => {
          const update = pendingConfirm;
          setPendingConfirm(null);
          if (update) void submit(update);
        }}
        onCancel={() => setPendingConfirm(null)}
      />
    </div>
  );
}
