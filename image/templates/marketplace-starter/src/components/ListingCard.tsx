import { Heart } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { cn } from "@/lib/utils"
import type { Listing } from "@/lib/types"

type Props = {
  listing: Listing
  favorite: boolean
  onOpen: (listing: Listing) => void
  onToggleFavorite: (id: string) => void
}

/** The photo slot: a real image when the listing has one, otherwise a
    designed monogram tile — never a broken-image icon. */
export function ListingPhoto({ listing, className }: { listing: Listing; className?: string }) {
  if (listing.photo) {
    return (
      <img
        src={listing.photo}
        alt={listing.title}
        className={cn("size-full object-cover", className)}
      />
    )
  }
  return (
    <div className={cn("flex size-full items-center justify-center bg-secondary", className)}>
      <span className="text-3xl font-semibold text-muted-foreground/60">
        {listing.title.slice(0, 1).toUpperCase()}
      </span>
    </div>
  )
}

export function ListingCard({ listing, favorite, onOpen, onToggleFavorite }: Props) {
  return (
    <Card className="group overflow-hidden pt-0 transition-shadow hover:shadow-md">
      <button
        className="aspect-[4/3] w-full overflow-hidden text-left"
        onClick={() => onOpen(listing)}
        aria-label={`Open ${listing.title}`}
      >
        <ListingPhoto listing={listing} className="transition-transform group-hover:scale-[1.02]" />
      </button>
      <CardContent className="space-y-1.5">
        <div className="flex items-start justify-between gap-2">
          <button className="text-left font-medium leading-snug" onClick={() => onOpen(listing)}>
            {listing.title}
          </button>
          <Button
            variant="ghost"
            size="icon"
            className="-mr-2 -mt-1 shrink-0"
            aria-label={favorite ? "Remove from saved" : "Save listing"}
            aria-pressed={favorite}
            onClick={() => onToggleFavorite(listing.id)}
          >
            <Heart className={cn("size-4", favorite && "fill-destructive text-destructive")} />
          </Button>
        </div>
        <div className="flex items-center justify-between text-sm">
          <span className="font-semibold tabular-nums">${listing.price}</span>
          <span className="text-muted-foreground">{listing.location}</span>
        </div>
        <Badge variant="outline">{listing.category}</Badge>
      </CardContent>
    </Card>
  )
}
