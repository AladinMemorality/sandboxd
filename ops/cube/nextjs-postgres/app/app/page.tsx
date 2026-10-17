import Notes from './notes';
export default function Page() {
  return <main><p className="eyebrow">YOUR NEW WORKSPACE</p><h1>Ready for your next idea.</h1>
    <p className="intro">A fresh Next.js app with a private PostgreSQL database. Start building, or save a note to try it out.</p>
    <Notes />
    <footer>Next.js 16.4.2 · React 19.3.1 · PostgreSQL 18.6</footer>
  </main>;
}
