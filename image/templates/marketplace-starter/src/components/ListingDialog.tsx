import { Heart, MapPin, User } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Separator } from "@/components/ui/separator"
import { cn } from "@/lib/utils"
import type { Listing } from "@/lib/types"
import { ListingPhoto } from "./ListingCard"

type Props = {
  listing: Listing | null
  favorite: boolean
  onClose: () => void
  onToggleFavorite: (id: string) => void
}

export function ListingDialog({ listing, favorite, onClose, onToggleFavorite }: Props) {
  return (
    <Dialog open={listing !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-lg overflow-hidden p-0">
        {listing && (
          <>
            <div className="aspect-[16/9] w-full overflow-hidden">
              <ListingPhoto listing={listing} />
            </div>
            <div className="space-y-3 p-6 pt-2">
              <DialogHeader>
                <div className="flex items-start justify-between gap-3">
                  <DialogTitle>{listing.title}</DialogTitle>
                  <span className="shrink-0 text-lg font-semibold tabular-nums">
                    ${listing.price}
                  </span>
                </div>
                <DialogDescription className="text-left">{listing.description}</DialogDescription>
              </DialogHeader>
              <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground">
                <span className="inline-flex items-center gap-1">
                  <MapPin className="size-3.5" aria-hidden /> {listing.location}
                </span>
                <span className="inline-flex items-center gap-1">
                  <User className="size-3.5" aria-hidden /> {listing.seller}
                </span>
                <Badge variant="outline">{listing.category}</Badge>
              </div>
              <Separator />
              <div className="flex gap-2">
                <Button className="flex-1">Message seller</Button>
                <Button
                  variant="outline"
                  aria-pressed={favorite}
                  onClick={() => onToggleFavorite(listing.id)}
                >
                  <Heart className={cn("size-4", favorite && "fill-destructive text-destructive")} />
                  {favorite ? "Saved" : "Save"}
                </Button>
              </div>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
