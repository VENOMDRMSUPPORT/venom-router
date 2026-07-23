import * as React from "react";
import { Icon } from "../icons/Icon";

let fieldSeq = 0;

export interface FormFieldProps {
  label: React.ReactNode;
  description?: React.ReactNode;
  /** Typed, actionable message — never a vague placeholder. */
  error?: React.ReactNode;
  required?: boolean;
  htmlFor?: string;
  /** Exactly one form control — receives `id`/`invalid`/`aria-describedby`/`aria-required`. */
  children: React.ReactElement<{ invalid?: boolean; id?: string } & React.AriaAttributes>;
  className?: string;
}

export function FormField(props: FormFieldProps) {
  const { label, description, error, required = false, htmlFor, children, className = "" } = props;
  const idRef = React.useRef<string>(htmlFor || "vnf-" + ++fieldSeq);
  const id = idRef.current;
  const descId = description ? id + "-desc" : undefined;
  const errId = error ? id + "-err" : undefined;
  const child = React.Children.only(children);
  const control = React.cloneElement(child, {
    id,
    invalid: !!error || child.props.invalid,
    "aria-describedby": [descId, errId].filter(Boolean).join(" ") || undefined,
    "aria-required": required || undefined,
  });
  return (
    <div className={("vn-field " + className).trim()}>
      <label className="vn-label" htmlFor={id}>{label}{required ? <span className="vn-field-required" aria-hidden="true"> *</span> : null}</label>
      {control}
      {description ? <div className="vn-field-desc" id={descId}>{description}</div> : null}
      {error ? <div className="vn-field-error" id={errId} role="alert"><Icon name="circle-alert" size={12} />{error}</div> : null}
    </div>
  );
}

export interface FieldDescriptionProps {
  children?: React.ReactNode;
  className?: string;
}

export function FieldDescription(props: FieldDescriptionProps) { return <div className={("vn-field-desc " + (props.className || "")).trim()}>{props.children}</div>; }

export interface FieldErrorProps {
  children?: React.ReactNode;
  className?: string;
}

export function FieldError(props: FieldErrorProps) { return <div className={("vn-field-error " + (props.className || "")).trim()} role="alert"><Icon name="circle-alert" size={12} />{props.children}</div>; }
