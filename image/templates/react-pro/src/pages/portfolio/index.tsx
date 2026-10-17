import { useState } from 'react'
import { Link, Routes, Route, useParams } from 'react-router-dom'
import { Shell, NotFound } from '@/components/layout/Shell'
import Contact from '@/pages/Contact'
import { site } from '@/content/site'
function Work() {
  const [category, setCategory] = useState('All')
  return <div className="container section"><p className="eyebrow">Independent design & direction</p><h1>Ideas made<br /><em>tangible.</em></h1><p className="lede">Selected explorations in identity, editorial and digital design.</p><div className="filter-row" role="group" aria-label="Project category">{['All', ...new Set(site.projects.map(p => p.category))].map(value => <button key={value} aria-pressed={category === value} onClick={() => setCategory(value)}>{value}</button>)}</div><div className="work-grid">{site.projects.filter(p => category === 'All' || p.category === category).map((project, i) => <Link key={project.id} to={`/work/${project.id}`} className="work-card"><div className="project-art" style={{ background: project.color }} aria-hidden="true"><span>{String(i + 1).padStart(2, '0')}</span><div /></div><div className="project-caption"><h2>{project.title}</h2><span>{project.category} ↗</span></div></Link>)}</div></div>
}
function Detail() {
  const { id } = useParams()
  const project = site.projects.find(p => p.id === id)
  if (!project) return <NotFound />
  return <article className="container section"><Link className="text-link" to="/">← All work</Link><p className="eyebrow mt-12">{project.category} / {project.year}</p><h1>{project.title}</h1><p className="lede">{project.description}</p><div className="project-art detail-art" style={{ background: project.color }} aria-hidden="true"><div /></div><section className="section"><h2>Space for a new perspective.</h2><p className="lede">A study in proportion, material and the small details that bring an idea together.</p><Link to="/contact" className="action">Start a conversation ↗</Link></section></article>
}
export default function Portfolio() { return <Shell links={[{ to: '/', label: 'Selected work' }, { to: '/contact', label: 'Get in touch' }]}><Routes><Route path="/" element={<Work />} /><Route path="/work/:id" element={<Detail />} /><Route path="/contact" element={<Contact />} /><Route path="*" element={<NotFound />} /></Routes></Shell> }
