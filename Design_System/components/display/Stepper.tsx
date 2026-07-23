import * as React from "react";
import { Icon } from "../icons/Icon";

export interface StepperProps {
  steps?: string[];
  /** Index of the active step; steps before it render as done. */
  current?: number;
  className?: string;
}

export function Stepper(props: StepperProps) {
  const { steps = [], current = 0, className = "" } = props;
  return (
    <div className={("vn-stepper " + className).trim()} role="list" aria-label="Progress">
      {steps.map((s, i) => {
        const state = i < current ? "done" : i === current ? "active" : "todo";
        return (
          <React.Fragment key={s}>
            {i > 0 ? <span className="vn-step-line" aria-hidden="true"></span> : null}
            <span className="vn-step" data-state={state} role="listitem" aria-current={state === "active" ? "step" : undefined}>
              <span className="vn-step-index">{state === "done" ? <Icon name="check" size={11} /> : i + 1}</span>
              {s}
            </span>
          </React.Fragment>
        );
      })}
    </div>
  );
}
