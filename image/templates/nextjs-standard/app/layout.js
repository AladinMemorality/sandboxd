// Minimal Next.js App Router layout (plain JS — no tsconfig bootstrap needed).
export const metadata = { title: 'Baarcha sandbox' }

export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body style={{ margin: 0 }}>{children}</body>
    </html>
  )
}
