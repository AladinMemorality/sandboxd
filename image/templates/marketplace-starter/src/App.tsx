import { useMemo, useState } from "react"
import { PackageOpen } from "lucide-react"
import { AppHeader } from "@/components/AppHeader"
import { FilterBar, type SortOrder } from "@/components/FilterBar"
import { ListingCard } from "@/components/ListingCard"
import { ListingDialog } from "@/components/ListingDialog"
import { NewListingDialog } from "@/components/NewListingDialog"
import { addListing, loadFavorites, loadListings, toggleFavorite } from "@/lib/store"
import type { Listing } from "@/lib/types"

export default function App() {
  const [listings, setListings] = useState<Listing[]>(loadListings)
  const [favorites, setFavorites] = useState<string[]>(loadFavorites)
  const [view, setView] = useState<"browse" | "favorites">("browse")
  const [query, setQuery] = useState("")
  const [category, setCategory] = useState("all")
  const [sort, setSort] = useState<SortOrder>("newest")
  const [openListing, setOpenListing] = useState<Listing | null>(null)
  const [selling, setSelling] = useState(false)

  const visible = useMemo(() => {
    let out = listings
    if (view === "favorites") out = out.filter((l) => favorites.includes(l.id))
    if (category !== "all") out = out.filter((l) => l.category === category)
    const q = query.trim().toLowerCase()
    if (q) {
      out = out.filter(
        (l) => l.title.toLowerCase().includes(q) || l.description.toLowerCase().includes(q)
      )
    }
    return [...out].sort((a, b) => {
      if (sort === "price-asc") return a.price - b.price
      if (sort === "price-desc") return b.price - a.price
      return b.createdAt.localeCompare(a.createdAt)
    })
  }, [listings, favorites, view, category, query, sort])

  const onToggleFavorite = (id: string) => setFavorites((f) => toggleFavorite(f, id))

  return (
    <div className="min-h-screen">
      <AppHeader
        view={view}
        favoriteCount={favorites.length}
        onViewChange={setView}
        onNewListing={() => setSelling(true)}
      />
      <main className="mx-auto max-w-6xl space-y-4 px-4 py-5">
        <FilterBar
          query={query}
          category={category}
          sort={sort}
          onQueryChange={setQuery}
          onCategoryChange={setCategory}
          onSortChange={setSort}
        />
        {visible.length === 0 ? (
          <div className="flex flex-col items-center gap-2 py-24 text-center">
            <PackageOpen className="size-8 text-muted-foreground/50" aria-hidden />
            <p className="font-medium">
              {view === "favorites" ? "Nothing saved yet" : "No listings match"}
            </p>
            <p className="text-sm text-muted-foreground">
              {view === "favorites"
                ? "Tap the heart on a listing to keep it here."
                : "Try a different search or category."}
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {visible.map((l) => (
              <ListingCard
                key={l.id}
                listing={l}
                favorite={favorites.includes(l.id)}
                onOpen={setOpenListing}
                onToggleFavorite={onToggleFavorite}
              />
            ))}
          </div>
        )}
      </main>
      <ListingDialog
        listing={openListing}
        favorite={openListing ? favorites.includes(openListing.id) : false}
        onClose={() => setOpenListing(null)}
        onToggleFavorite={onToggleFavorite}
      />
      <NewListingDialog
        open={selling}
        onClose={() => setSelling(false)}
        onCreate={(l) => setListings((ls) => addListing(ls, l))}
      />
    </div>
  )
}
