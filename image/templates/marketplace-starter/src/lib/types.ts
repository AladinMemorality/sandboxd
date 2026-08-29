export const CATEGORIES = ["Electronics", "Home", "Fashion", "Sports", "Books", "Other"] as const
export type Category = (typeof CATEGORIES)[number]

export type Listing = {
  id: string
  title: string
  description: string
  price: number
  category: Category
  location: string
  seller: string
  /** Optional image URL or imported asset; absent = designed placeholder. */
  photo?: string
  createdAt: string
}
