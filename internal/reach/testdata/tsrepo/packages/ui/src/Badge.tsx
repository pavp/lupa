export function Badge(label: string, size: "sm" | "md"): string {
  return `[${size}] ${label}`;
}
