/**
 * @venom/design-system — main entry point.
 *
 * Consumers may import everything from here, or use the narrower subpath exports
 * (`@venom/design-system/tokens`, `/themes`, `/density`, `/customizer`, `/icons`,
 * `/primitives`, `/domain`) documented in README.md and validation/handoff-contract.md.
 * `styles.css` is not re-exported here — link it directly:
 * `import "@venom/design-system/styles.css"`.
 */
export * from "./primitives";
export * from "./domain";
export * from "./icons";
export * from "./tokens";
export * from "./themes";
export * from "./density";
export * from "./customizer";
