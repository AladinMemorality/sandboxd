# The component kit

Pre-built, token-driven primitives. IMPORT them; do not rewrite, restyle,
or fork them. One-off adjustments go through `className`; theme changes go
through the token variables in `src/index.css`.

    import { Button } from "@/components/ui/button"
    import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
    import { Dialog, DialogContent, DialogTrigger, DialogHeader, DialogTitle } from "@/components/ui/dialog"
    import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
    import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
    import { toast } from "sonner"

App-specific components belong in `src/components/`, composed from these.
