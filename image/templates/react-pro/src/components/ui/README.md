# The component kit

Pre-built, token-driven primitives. Import and compose them. One-off
adjustments go through `className`; justified shared variants belong in the
primitive. Theme changes go
through the token variables in `src/index.css`.

    import { Button } from "@/components/ui/button"
    import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
    import { Dialog, DialogContent, DialogTrigger, DialogHeader, DialogTitle } from "@/components/ui/dialog"
    import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
    import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
    import { toast } from "sonner"

App-specific components belong in `src/components/`, composed from these.

## Common APIs

- `Button`: native button props, `asChild`, `variant` (default, outline,
  secondary, ghost, link, destructive), `size` (default, xs, sm, lg, icon,
  icon-xs, icon-sm, icon-lg). Example: `<Button asChild><a href="#features">Explore</a></Button>`.
- `Card`, `CardHeader`, `CardTitle`, `CardDescription`, `CardContent`,
  `CardFooter`: compose ordinary content; `className` adjusts layout.
- `Input`, `Textarea`, `Label`: native field props; connect label `htmlFor`
  with input `id`, give validation errors readable text.
- `Dialog`: `open`/`onOpenChange`; put the opening control inside
  `DialogTrigger asChild`, content inside `DialogContent`, and include
  `DialogTitle` and `DialogDescription` for accessibility.
- `Tabs`: `defaultValue` or `value`/`onValueChange`; match the `value` of
  each `TabsTrigger` with its `TabsContent`.
- `Select`: `value`/`onValueChange`; pair `SelectTrigger`/`SelectValue` with
  `SelectContent` containing `SelectItem value="..."`.

This describes the shipped kit. Use the runtime export map to see current
exports; inspect relevant source when an existing project has changed the API.

## Layout and identity

Tailwind v4 is already compiled by Vite; no setup or config generation needed.
Use semantic colors (`bg-background`, `text-foreground`, `bg-primary`), responsive
utilities and natural document flow. For example:

```tsx
<main className="mx-auto w-full max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
  <section className="grid gap-8 lg:grid-cols-2 lg:items-center">
    {/* Compose the requested content and controls; this is not a required layout. */}
  </section>
</main>
```

Prefer utilities for spacing, layout and breakpoints. Theme only the relevant
tokens in `src/index.css`; retain the Tailwind imports and semantic mappings.
Use custom CSS when it is clearer for special effects or selectors. Distinct
apps should have distinct content hierarchy, typography, composition and imagery;
reusing a button does not require reusing an entire page design.
