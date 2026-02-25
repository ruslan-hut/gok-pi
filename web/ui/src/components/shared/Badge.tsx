import type { CSSProperties, ReactNode } from "react";

interface BadgeProps {
  children: ReactNode;
  variant?: "online" | "offline" | "disabled" | "default";
  style?: CSSProperties;
}

export function Badge({
  children,
  variant = "default",
  style,
}: BadgeProps) {
  return (
    <span
      className={`badge${variant !== "default" ? ` ${variant}` : ""}`}
      style={style}
    >
      {children}
    </span>
  );
}
