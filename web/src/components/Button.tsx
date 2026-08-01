import { forwardRef } from "react";
import type { ButtonHTMLAttributes } from "react";

export type ButtonVariant = "default" | "primary" | "danger" | "ghost";
export type ButtonSize = "md" | "sm";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
}

/**
 * Shared button rendering the .btn class system from styles/components.css.
 * Variant and size map to modifier classes: `btn primary sm`. Extra
 * className values are appended after the modifiers.
 */
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "default", size = "md", className, ...rest },
  ref,
) {
  const classes = ["btn"];
  if (variant !== "default") classes.push(variant);
  if (size !== "md") classes.push(size);
  if (className) classes.push(className);
  return <button ref={ref} className={classes.join(" ")} {...rest} />;
});
