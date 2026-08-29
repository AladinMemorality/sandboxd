import { useState } from "react"
import { toast } from "sonner"
import { z } from "zod"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { CATEGORIES, type Listing } from "@/lib/types"

const draftSchema = z.object({
  title: z.string().trim().min(3, "Give it a title of at least 3 characters."),
  description: z.string().trim().min(10, "Describe it in at least 10 characters."),
  price: z.coerce.number().positive("Price must be a positive number."),
  category: z.enum(CATEGORIES),
  location: z.string().trim().min(2, "Where can buyers pick it up?"),
})

type Props = {
  open: boolean
  onClose: () => void
  onCreate: (listing: Listing) => void
}

const EMPTY = { title: "", description: "", price: "", category: "" as string, location: "" }

export function NewListingDialog({ open, onClose, onCreate }: Props) {
  const [draft, setDraft] = useState(EMPTY)
  const [errors, setErrors] = useState<Record<string, string>>({})

  const set = (field: keyof typeof EMPTY) => (value: string) =>
    setDraft((d) => ({ ...d, [field]: value }))

  function submit() {
    const parsed = draftSchema.safeParse(draft)
    if (!parsed.success) {
      const next: Record<string, string> = {}
      for (const issue of parsed.error.issues) next[String(issue.path[0])] = issue.message
      setErrors(next)
      return
    }
    onCreate({
      id: `l${Date.now()}`,
      ...parsed.data,
      seller: "You",
      createdAt: new Date().toISOString(),
    })
    setDraft(EMPTY)
    setErrors({})
    onClose()
    toast.success("Listing published")
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Sell something</DialogTitle>
          <DialogDescription>It goes live in the marketplace immediately.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="nl-title">Title</Label>
            <Input id="nl-title" value={draft.title} onChange={(e) => set("title")(e.target.value)} />
            {errors.title && <p className="text-sm text-destructive">{errors.title}</p>}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="nl-desc">Description</Label>
            <Textarea
              id="nl-desc"
              rows={3}
              value={draft.description}
              onChange={(e) => set("description")(e.target.value)}
            />
            {errors.description && <p className="text-sm text-destructive">{errors.description}</p>}
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="nl-price">Price ($)</Label>
              <Input
                id="nl-price"
                inputMode="decimal"
                value={draft.price}
                onChange={(e) => set("price")(e.target.value)}
              />
              {errors.price && <p className="text-sm text-destructive">{errors.price}</p>}
            </div>
            <div className="space-y-1.5">
              <Label>Category</Label>
              <Select value={draft.category} onValueChange={set("category")}>
                <SelectTrigger aria-label="Category">
                  <SelectValue placeholder="Pick one" />
                </SelectTrigger>
                <SelectContent>
                  {CATEGORIES.map((c) => (
                    <SelectItem key={c} value={c}>
                      {c}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {errors.category && <p className="text-sm text-destructive">{errors.category}</p>}
            </div>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="nl-loc">Pickup location</Label>
            <Input
              id="nl-loc"
              value={draft.location}
              onChange={(e) => set("location")(e.target.value)}
            />
            {errors.location && <p className="text-sm text-destructive">{errors.location}</p>}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit}>Publish</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
