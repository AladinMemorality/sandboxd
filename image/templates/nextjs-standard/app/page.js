// The waiting screen; replaced entirely by the first real build.
export default function Home() {
  return (
    <main
      style={{
        display: 'grid',
        placeContent: 'center',
        justifyItems: 'center',
        minHeight: '100vh',
        padding: 32,
        background: '#f4efe6',
        color: '#23201b',
        fontFamily: '"Helvetica Neue", -apple-system, "Segoe UI", Arial, sans-serif',
        textAlign: 'center',
      }}
    >
      <p
        style={{
          margin: '0 0 12px',
          color: '#a3372e',
          fontSize: 13,
          fontWeight: 700,
          letterSpacing: '0.14em',
          textTransform: 'uppercase',
        }}
      >
        Baarcha sandbox
      </p>
      <h1
        style={{
          margin: 0,
          fontFamily: 'Georgia, "Times New Roman", serif',
          fontSize: 'clamp(30px, 6vw, 46px)',
          lineHeight: 1.15,
        }}
      >
        Hannibal is on it.
      </h1>
      <p style={{ maxWidth: 420, margin: '14px 0 0', color: '#6d675e', lineHeight: 1.6 }}>
        This workspace was just created. Ask for changes in the chat and watch
        them land here live.
      </p>
    </main>
  )
}
