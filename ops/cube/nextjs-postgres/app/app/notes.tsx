"use client";
import { useEffect, useState } from 'react';
type Note = {id: string; body: string};
export default function Notes() {
  const [notes, setNotes] = useState<Note[]>([]);
  const [body, setBody] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => { fetch('/api/notes').then(async r => { if (!r.ok) throw new Error(); setNotes(await r.json()); }).catch(() => setError('The database is still starting. Try again shortly.')); }, []);
  async function save(event: React.FormEvent) {
    event.preventDefault(); setPending(true); setError('');
    try {
      const r = await fetch('/api/notes', {method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({body})});
      if (!r.ok) throw new Error();
      const note = await r.json(); setNotes(old => [note, ...old].slice(0,20)); setBody('');
    } catch { setError('Could not save your note. Please try again.'); }
    finally { setPending(false); }
  }
  return <section><form onSubmit={save}><label htmlFor="note">Try saving something</label>
    <div className="input-row"><input id="note" value={body} onChange={e => setBody(e.target.value)} placeholder="Your first idea…" maxLength={500} required />
    <button disabled={pending || !body.trim()}>{pending ? 'Saving…' : 'Save note'}</button></div>
    <p role="status">{error || (notes.length ? 'Your notes are saved in PostgreSQL.' : 'Saved notes stay here when the sandbox restarts.')}</p>
  </form><ul>{notes.map(note => <li key={note.id}>{note.body}</li>)}</ul></section>;
}
