import './globals.css';
export const metadata = { title: 'Next.js sandbox', description: 'A prepared Next.js and PostgreSQL development environment.' };
export default function Layout({children}: {children: React.ReactNode}) {
  return <html lang="en"><body>{children}</body></html>;
}
