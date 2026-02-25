interface IconProps {
  name: string;
  size?: number;
  className?: string;
}

export function Icon({ name, size, className }: IconProps) {
  return (
    <span
      className={`material-symbols-outlined${className ? ` ${className}` : ""}`}
      style={size ? { fontSize: size } : undefined}
      aria-hidden="true"
    >
      {name}
    </span>
  );
}
