import { Toaster as Sonner, type ToasterProps } from "sonner"

/* Theme follows the .dark class on <html> (this template's dark-mode
   convention) rather than a theme provider. */
const Toaster = ({ ...props }: ToasterProps) => {
  const theme = typeof document !== "undefined" &&
    document.documentElement.classList.contains("dark")
    ? "dark"
    : "light"
  return (
    <Sonner
      theme={theme}
      className="toaster group"
      style={{ "--normal-bg": "var(--popover)", "--normal-text": "var(--popover-foreground)", "--normal-border": "var(--border)" } as React.CSSProperties}
      {...props}
    />
  )
}

export { Toaster }
