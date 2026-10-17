import { useEffect, type RefObject } from 'react'
import { useReducedMotion } from 'motion/react'
import type { gsap as GSAP } from 'gsap'
// Memoize setup with useCallback. It owns only animations inside this scope.
export function useScrollScene(scope: RefObject<HTMLElement>, setup: (gsap: typeof GSAP) => void) {
  const reduced = useReducedMotion()
  useEffect(() => {
    if (reduced || !scope.current) return
    let cancelled = false
    let context: ReturnType<typeof GSAP.context> | undefined
    void Promise.all([import('gsap'), import('gsap/ScrollTrigger')]).then(([{ gsap }, { ScrollTrigger }]) => {
      if (cancelled || !scope.current) return
      gsap.registerPlugin(ScrollTrigger)
      context = gsap.context(() => setup(gsap), scope.current)
    }).catch(console.error)
    return () => { cancelled = true; context?.revert() }
  }, [scope, setup, reduced])
}
