import * as React from "react";

export interface TextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {
  invalid?: boolean;
}

export function Textarea(props: TextareaProps) {
  const { invalid = false, className = "", rows = 3, ...rest } = props;
  return <textarea className={("vn-textarea " + className).trim()} rows={rows} aria-invalid={invalid || undefined} {...rest} />;
}
