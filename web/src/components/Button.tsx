import { forwardRef } from "react";
import type { ButtonHTMLAttributes } from "react";

export type ButtonVariant = "default" | "primary" | "danger" | "ghost";
export type ButtonSize = "md" | "sm";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
}

/**
 * 共享按钮，渲染 styles/components.css 的 .btn 类系统。
 *
 * 两色制（见设计系统约定）：Modernist 调色板没有独立的危险色，accent 本身
 * 就是朱红，所以 danger 复用 .btn-primary 的外观，另加一个语义化的
 * .btn-danger 标记 —— 代码里的意图保持可读，将来要区分也有接缝。
 */
const VARIANT_CLASSES: Record<ButtonVariant, string[]> = {
  default: ["btn-secondary"],
  primary: ["btn-primary"],
  danger: ["btn-primary", "btn-danger"],
  ghost: ["btn-ghost"],
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "default", size = "md", className, ...rest },
  ref,
) {
  const classes = ["btn", ...VARIANT_CLASSES[variant]];
  if (size !== "md") classes.push(`btn-${size}`);
  if (className) classes.push(className);
  return <button ref={ref} className={classes.join(" ")} {...rest} />;
});
