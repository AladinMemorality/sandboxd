import { lazy, Suspense, Component, type ReactNode } from 'react'
import { BrowserRouter } from 'react-router-dom'
import { MotionConfig } from 'motion/react'
import starter from './content/starter.json'

const compositions = {
  marketing: lazy(() => import('./pages/marketing')),
  portfolio: lazy(() => import('./pages/portfolio')),
  app: lazy(() => import('./pages/app')),
}
class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  render() {
    return this.state.failed ? <main className="container section"><h1>Something went wrong.</h1><p>Please reload the page to try again.</p><button className="action" onClick={() => window.location.reload()}>Reload</button></main> : this.props.children
  }
}
export default function App() {
  const Pages = compositions[starter.composition as keyof typeof compositions] ?? compositions.marketing
  return <ErrorBoundary><MotionConfig reducedMotion="user"><BrowserRouter><Suspense fallback={<p className="container section" role="status">Loading…</p>}><Pages /></Suspense></BrowserRouter></MotionConfig></ErrorBoundary>
}
