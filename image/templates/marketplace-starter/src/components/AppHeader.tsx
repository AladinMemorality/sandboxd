import { Heart, Plus, Store } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"

type Props = {
  view: "browse" | "favorites"
  favoriteCount: number
  onViewChange: (view: "browse" | "favorites") => void
  onNewListing: () => void
}

export function AppHeader({ view, favoriteCount, onViewChange, onNewListing }: Props) {
  return (
    <header className="sticky top-0 z-40 border-b bg-background/95 backdrop-blur">
      <div className="mx-auto flex h-14 max-w-6xl items-center gap-3 px-4">
        <button
          className="flex items-center gap-2 font-semibold"
          onClick={() => onViewChange("browse")}
        >
          <Store className="size-5" aria-hidden />
          Marketplace
        </button>
        <div className="ml-auto flex items-center gap-2">
          <Button
            variant={view === "favorites" ? "secondary" : "ghost"}
            size="sm"
            onClick={() => onViewChange(view === "favorites" ? "browse" : "favorites")}
          >
            <Heart className="size-4" aria-hidden />
            Saved
            {favoriteCount > 0 && (
              <Badge variant="secondary" className="ml-1 px-1.5">
                {favoriteCount}
              </Badge>
            )}
          </Button>
          <Button size="sm" onClick={onNewListing}>
            <Plus className="size-4" aria-hidden />
            Sell something
          </Button>
        </div>
      </div>
    </header>
  )
}
