import * as React from "react";
export interface FormFieldProps {
    label: React.ReactNode;
    description?: React.ReactNode;
    /** Typed, actionable message — never a vague placeholder. */
    error?: React.ReactNode;
    required?: boolean;
    htmlFor?: string;
    /** Exactly one form control — receives `id`/`invalid`/`aria-describedby`/`aria-required`. */
    children: React.ReactElement<{
        invalid?: boolean;
        id?: string;
    } & React.AriaAttributes>;
    className?: string;
}
export declare function FormField(props: FormFieldProps): React.JSX.Element;
export interface FieldDescriptionProps {
    children?: React.ReactNode;
    className?: string;
}
export declare function FieldDescription(props: FieldDescriptionProps): React.JSX.Element;
export interface FieldErrorProps {
    children?: React.ReactNode;
    className?: string;
}
export declare function FieldError(props: FieldErrorProps): React.JSX.Element;
