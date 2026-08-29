import type { Listing } from "./types"

/* Realistic seed data so the app is alive on first boot. Replace wholesale
   when customizing to a real domain. */
export const SEED_LISTINGS: Listing[] = [
  { id: "l1", title: "Vintage road bike, 56cm", description: "Steel frame, freshly serviced, new tires. Rides like a dream.", price: 240, category: "Sports", location: "Riverside", seller: "Maya K.", createdAt: "2026-08-20T10:00:00Z" },
  { id: "l2", title: "MacBook Air M2, 16GB", description: "Light scratches on the lid, battery at 91%. Charger included.", price: 780, category: "Electronics", location: "Old Town", seller: "Youssef B.", createdAt: "2026-08-21T09:30:00Z" },
  { id: "l3", title: "Mid-century oak sideboard", description: "Solid oak, two doors and three drawers. Pickup only.", price: 320, category: "Home", location: "Harbor District", seller: "Lena M.", createdAt: "2026-08-21T15:20:00Z" },
  { id: "l4", title: "Espresso machine + grinder", description: "Prosumer setup, descaled monthly. Selling as a pair.", price: 410, category: "Home", location: "Riverside", seller: "Omar T.", createdAt: "2026-08-22T08:05:00Z" },
  { id: "l5", title: "Wool winter coat, size M", description: "Worn one season. Deep navy, fully lined.", price: 65, category: "Fashion", location: "Old Town", seller: "Sofia R.", createdAt: "2026-08-22T18:45:00Z" },
  { id: "l6", title: "Nintendo Switch OLED", description: "Complete in box with two games. Barely used.", price: 210, category: "Electronics", location: "Northside", seller: "Karim D.", createdAt: "2026-08-23T11:10:00Z" },
  { id: "l7", title: "Climbing shoes, EU 42", description: "Resoled once, plenty of grip left. Great starter pair.", price: 45, category: "Sports", location: "Harbor District", seller: "Anna P.", createdAt: "2026-08-23T19:00:00Z" },
  { id: "l8", title: "Complete Tintin collection", description: "All 24 albums, hardcover, very good condition.", price: 150, category: "Books", location: "Old Town", seller: "Marc L.", createdAt: "2026-08-24T09:15:00Z" },
  { id: "l9", title: "Standing desk, 120×70", description: "Electric height adjustment, memory presets. Disassembles flat.", price: 260, category: "Home", location: "Northside", seller: "Ines V.", createdAt: "2026-08-24T14:30:00Z" },
  { id: "l10", title: "Sony WH-1000XM5", description: "Noise cancelling headphones, case and cable included.", price: 190, category: "Electronics", location: "Riverside", seller: "Tom H.", createdAt: "2026-08-25T10:40:00Z" },
  { id: "l11", title: "Leather satchel", description: "Full-grain leather, ages beautifully. Fits a 14-inch laptop.", price: 85, category: "Fashion", location: "Harbor District", seller: "Nadia F.", createdAt: "2026-08-25T16:55:00Z" },
  { id: "l12", title: "Cast iron cookware set", description: "Skillet, dutch oven, griddle. Seasoned and ready.", price: 110, category: "Home", location: "Old Town", seller: "Piotr W.", createdAt: "2026-08-26T12:00:00Z" },
]
