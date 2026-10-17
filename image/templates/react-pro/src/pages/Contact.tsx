import { useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { site } from '@/content/site'
export default function Contact() {
  const [params] = useSearchParams()
  const [draft, setDraft] = useState('')
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const body = `From: ${data.get('name')} <${data.get('email')}>\n\n${data.get('message')}`
    setDraft(`mailto:${site.email}?subject=${encodeURIComponent('Let’s talk' + (params.get('plan') ? `: ${params.get('plan')}` : ''))}&body=${encodeURIComponent(body)}`)
  }
  return <div className="container section contact-grid"><div><p className="eyebrow">Start a conversation</p><h1>What’s on<br />your mind?</h1><p className="lede">Tell us a little about your idea.</p><a href={`mailto:${site.email}`}>{site.email}</a></div>
    <form onSubmit={submit} className="form-stack"><label>Your name<input name="name" autoComplete="name" required /></label><label>Email address<input name="email" type="email" autoComplete="email" required /></label><label>Your message<textarea name="message" rows={5} defaultValue={params.get('plan') ? `I’m interested in the ${params.get('plan')} plan.` : ''} required /></label><button className="action" type="submit">Prepare email</button><p className="fine-print">This form prepares a draft in your email app. Nothing is sent from this page.</p>{draft && <div role="status"><a className="text-link" href={draft}>Open your email draft ↗</a></div>}</form>
  </div>
}
