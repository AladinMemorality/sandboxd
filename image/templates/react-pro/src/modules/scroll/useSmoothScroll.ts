import { useEffect } from 'react'
import { useReducedMotion } from 'motion/react'
// Mount once, only for briefs that ask for smooth scrolling. Native by default.
export function useSmoothScroll(enabled = false) {
  const reduced = useReducedMotion()
  useEffect(() => {
    if (!enabled || reduced) return
    let cancelled = false
    let instance: import('lenis').default | undefined
    void import('lenis').then(({ default: Lenis }) => {
      if (!cancelled) instance = new Lenis({ autoRaf: true })
    }).catch(console.error)
    return () => { cancelled = true; instance?.destroy() }
  }, [enabled, reduced])
}
