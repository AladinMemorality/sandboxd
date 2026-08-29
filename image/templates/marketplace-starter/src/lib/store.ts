import { SEED_LISTINGS } from "./seed"
import type { Listing } from "./types"

/* localStorage-backed store: listings + favorites. Seeded on first load so
   the app is never empty. Swap for an API by reimplementing these five
   functions — components only know this interface. */

const LISTINGS_KEY = "marketplace.listings.v1"
const FAVORITES_KEY = "marketplace.favorites.v1"

function read<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key)
    return raw ? (JSON.parse(raw) as T) : fallback
  } catch {
    return fallback
  }
}

function write(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    /* storage full or blocked — the session state still works in memory */
  }
}

export function loadListings(): Listing[] {
  const existing = read<Listing[] | null>(LISTINGS_KEY, null)
  if (existing && existing.length) return existing
  write(LISTINGS_KEY, SEED_LISTINGS)
  return SEED_LISTINGS
}

export function saveListings(listings: Listing[]) {
  write(LISTINGS_KEY, listings)
}

export function addListing(listings: Listing[], listing: Listing): Listing[] {
  const next = [listing, ...listings]
  saveListings(next)
  return next
}

export function loadFavorites(): string[] {
  return read<string[]>(FAVORITES_KEY, [])
}

export function toggleFavorite(favorites: string[], id: string): string[] {
  const next = favorites.includes(id) ? favorites.filter((f) => f !== id) : [...favorites, id]
  write(FAVORITES_KEY, next)
  return next
}
