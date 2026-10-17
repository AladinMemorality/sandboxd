import { createContext, useContext, useState, type FormEvent } from 'react'
import { Link, Routes, Route, useParams } from 'react-router-dom'
import { Plus, ArrowUpRight } from 'lucide-react'
import { Shell, NotFound } from '@/components/layout/Shell'
type Project = { id: string; name: string; status: 'In progress' | 'Complete'; note: string }
const initial: Project[] = [
  { id: 'launch', name: 'Spring launch', status: 'In progress', note: 'Bring the first collection together.' },
  { id: 'research', name: 'Field research', status: 'In progress', note: 'Collect references and explore the next direction.' },
  { id: 'identity', name: 'Studio identity', status: 'Complete', note: 'A fresh start for the studio.' },
]
const Context = createContext<{ projects: Project[]; setProjects: React.Dispatch<React.SetStateAction<Project[]>>; name: string; setName: (name: string) => void } | null>(null)
const useWorkspace = () => useContext(Context)!
function Dashboard() {
  const { projects, name } = useWorkspace()
  return <div className="container section"><p className="eyebrow">Your workspace</p><h1>{name}.</h1><p className="lede">A clear view of what’s moving forward.</p><div className="stats"><div><strong>{projects.length}</strong><span>Projects</span></div><div><strong>{projects.filter(p => p.status === 'In progress').length}</strong><span>In progress</span></div><div><strong>{projects.filter(p => p.status === 'Complete').length}</strong><span>Complete</span></div></div><div className="section-heading"><h2>Recent projects</h2><Link className="text-link" to="/projects">View all <ArrowUpRight size={16} /></Link></div><ProjectList projects={projects.slice(0, 3)} /></div>
}
function ProjectList({ projects }: { projects: Project[] }) { return projects.length ? <div className="project-list">{projects.map(project => <Link to={`/projects/${project.id}`} key={project.id}><div><h3>{project.name}</h3><p>{project.note}</p></div><span className="status-tag">{project.status}</span></Link>)}</div> : <p className="empty-state" role="status">No projects found. Try a different search or add your first project.</p> }
function Projects() {
  const { projects, setProjects } = useWorkspace()
  const [search, setSearch] = useState('')
  function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = event.currentTarget; const name = String(new FormData(form).get('name') ?? '').trim()
    if (!name) return
    setProjects(items => [{ id: crypto.randomUUID(), name, status: 'In progress', note: 'Ready for your next step.' }, ...items]); form.reset()
  }
  return <div className="container section"><p className="eyebrow">Make something happen</p><h1>Projects.</h1><form className="add-project" onSubmit={add}><label className="sr-only" htmlFor="project-name">New project name</label><input id="project-name" name="name" placeholder="Name your next project" required maxLength={120} /><button className="action"><Plus size={18} />Add project</button></form><label className="search-label">Find a project<input type="search" value={search} onChange={event => setSearch(event.target.value)} placeholder="Search projects" /></label><ProjectList projects={projects.filter(p => p.name.toLowerCase().includes(search.toLowerCase()))} /></div>
}
function Detail() {
  const { projects, setProjects } = useWorkspace(); const { id } = useParams(); const project = projects.find(p => p.id === id)
  if (!project) return <NotFound />
  return <div className="container section"><Link className="text-link" to="/projects">← Projects</Link><p className="eyebrow mt-12">{project.status}</p><h1>{project.name}</h1><p className="lede">{project.note}</p><button className="action" onClick={() => setProjects(items => items.map(p => p.id === id ? { ...p, status: p.status === 'Complete' ? 'In progress' : 'Complete' } : p))}>{project.status === 'Complete' ? 'Reopen project' : 'Mark complete'}</button></div>
}
function Settings() {
  const { name, setName } = useWorkspace(); const [saved, setSaved] = useState(false)
  return <div className="container section"><p className="eyebrow">Make it your own</p><h1>Settings.</h1><form className="form-stack settings-form" onSubmit={event => { event.preventDefault(); const value = String(new FormData(event.currentTarget).get('name') ?? '').trim(); if (value) { setName(value); setSaved(true) } }}><label>Workspace name<input name="name" defaultValue={name} required maxLength={80} onChange={() => setSaved(false)} /></label><button className="action">Save changes</button>{saved && <p role="status">Workspace name updated.</p>}</form></div>
}
export default function AppPages() {
  const [projects, setProjects] = useState(initial); const [name, setName] = useState('Good things ahead')
  return <Context.Provider value={{ projects, setProjects, name, setName }}><Shell links={[{ to: '/', label: 'Overview' }, { to: '/projects', label: 'Projects' }, { to: '/settings', label: 'Settings' }]}><div className="container demo-note">Example workspace · Changes last for this session.</div><Routes><Route path="/" element={<Dashboard />} /><Route path="/projects" element={<Projects />} /><Route path="/projects/:id" element={<Detail />} /><Route path="/settings" element={<Settings />} /><Route path="*" element={<NotFound />} /></Routes></Shell></Context.Provider>
}
