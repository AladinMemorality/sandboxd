import { useEffect, useState, type ReactNode } from 'react'
import { Link, NavLink, useLocation } from 'react-router-dom'
import { Menu } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle, DialogDescription, DialogTrigger } from '@/components/ui/dialog'
import { site } from '@/content/site'
export function Shell({ links, children }: { links: Array<{ to: string; label: string }>; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  const { pathname } = useLocation()
  useEffect(() => { setOpen(false); window.scrollTo(0, 0); document.getElementById('main')?.focus({ preventScroll: true }) }, [pathname])
  const navigation = links.map(link => <NavLink key={link.to} to={link.to} end className={({ isActive }) => isActive ? 'nav-link active' : 'nav-link'}>{link.label}</NavLink>)
  return <><a href="#main" className="skip-link">Skip to content</a>
    <header className="container site-header"><Link to="/" className="site-name">{site.name}<span aria-hidden="true">.</span></Link><nav aria-label="Main" className="desktop-nav">{navigation}</nav>
      <Dialog open={open} onOpenChange={setOpen}><DialogTrigger asChild><button className="mobile-menu icon-button" aria-label="Open menu"><Menu size={22} /></button></DialogTrigger><DialogContent><DialogTitle>Explore</DialogTitle><DialogDescription>Choose a page.</DialogDescription><nav aria-label="Mobile" className="flex flex-col gap-3">{navigation}</nav></DialogContent></Dialog>
    </header><main id="main" tabIndex={-1}>{children}</main>
    <footer className="container site-footer"><span>{site.name} / {new Date().getFullYear()}</span><a href={`mailto:${site.email}`}>{site.email}</a></footer></>
}
export function NotFound() { return <div className="container section"><p className="eyebrow">404</p><h1>This page isn’t here.</h1><Link className="action" to="/">Back home</Link></div> }
