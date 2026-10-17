import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useReducedMotion } from 'motion/react'
import type * as THREE from 'three'
type SceneResult = { scene: THREE.Scene; camera: THREE.Camera; frame?: (seconds: number) => void; dispose: () => void }
// Memoize create. It owns geometry/material/texture disposal through dispose().
// This component owns renderer, listeners and animation frames.
export function Scene({ create, fallback, label }: { create: (three: typeof THREE) => SceneResult; fallback: ReactNode; label: string }) {
  const host = useRef<HTMLDivElement>(null)
  const reduced = useReducedMotion()
  const [ready, setReady] = useState(false)
  useEffect(() => {
    const element = host.current
    if (!element || reduced) return
    let cancelled = false, visible = false, starting = false, frame = 0
    let renderer: THREE.WebGLRenderer | undefined, result: SceneResult | undefined
    const resize = new ResizeObserver(() => {
      if (!renderer || !result) return
      renderer.setSize(element.clientWidth, element.clientHeight, false)
      if ('aspect' in result.camera) { (result.camera as THREE.PerspectiveCamera).aspect = element.clientWidth / Math.max(1, element.clientHeight); (result.camera as THREE.PerspectiveCamera).updateProjectionMatrix() }
    })
    const tick = (time: number) => {
      frame = 0
      if (cancelled || !visible || document.hidden || !renderer || !result) return
      result.frame?.(time / 1000); renderer.render(result.scene, result.camera)
      frame = requestAnimationFrame(tick)
    }
    const resume = () => { if (!frame && visible && !document.hidden) frame = requestAnimationFrame(tick) }
    const observer = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting
      if (!visible) { cancelAnimationFrame(frame); frame = 0; return }
      if (renderer) { resume(); return }
      if (starting) return
      starting = true
      void import('three').then(three => {
        if (cancelled) return
        renderer = new three.WebGLRenderer({ alpha: true, antialias: true })
        renderer.setPixelRatio(Math.min(window.devicePixelRatio, 1.5))
        result = create(three)
        element.appendChild(renderer.domElement); resize.observe(element)
        setReady(true); resume()
      }).catch(() => { renderer?.dispose(); result?.dispose(); renderer?.domElement.remove(); renderer = undefined; result = undefined; if (!cancelled) setReady(false) })
    })
    observer.observe(element); document.addEventListener('visibilitychange', resume)
    return () => { cancelled = true; observer.disconnect(); resize.disconnect(); document.removeEventListener('visibilitychange', resume); cancelAnimationFrame(frame); result?.dispose(); renderer?.dispose(); renderer?.domElement.remove(); setReady(false) }
  }, [create, reduced])
  return <div className="scene" role="img" aria-label={label}><div ref={host} className="scene-canvas" />{(!ready || reduced) && <div className="scene-fallback">{fallback}</div>}</div>
}
