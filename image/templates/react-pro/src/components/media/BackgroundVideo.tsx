import { useEffect, useRef, useState } from 'react'
import { useReducedMotion } from 'motion/react'

// Explicit type supports signed HLS URLs without a .m3u8 extension.
export function BackgroundVideo({ src, poster, label, type = 'mp4', position = 'center' }: {
  src: string; poster: string; label: string; type?: 'mp4' | 'hls'; position?: string
}) {
  const ref = useRef<HTMLVideoElement>(null)
  const reduced = useReducedMotion()
  const paused = useRef(false)
  const [failed, setFailed] = useState(false)
  const [playing, setPlaying] = useState(false)
  useEffect(() => {
    const video = ref.current
    if (!video) return
    let cancelled = false, visible = true
    let hls: import('hls.js').default | undefined
    setFailed(false)
    const sync = () => {
      if (cancelled) return
      if (reduced || paused.current || document.hidden || !visible) video.pause()
      else void video.play().catch(() => { if (!cancelled) setPlaying(false) })
    }
    const observer = new IntersectionObserver(entries => { visible = entries[0].isIntersecting; sync() })
    observer.observe(video)
    document.addEventListener('visibilitychange', sync)
    video.addEventListener('canplay', sync)
    if (!reduced) {
      if (type === 'hls' && !video.canPlayType('application/vnd.apple.mpegurl')) {
        void import('hls.js').then(({ default: Hls }) => {
          if (cancelled) return
          if (!Hls.isSupported()) { setFailed(true); return }
          hls = new Hls()
          hls.on(Hls.Events.ERROR, (_event, data) => { if (data.fatal) { setFailed(true); hls?.destroy() } })
          hls.loadSource(src); hls.attachMedia(video)
        }).catch(() => { if (!cancelled) setFailed(true) })
      } else { video.src = src; video.load() }
    }
    return () => { cancelled = true; observer.disconnect(); document.removeEventListener('visibilitychange', sync); video.removeEventListener('canplay', sync); hls?.destroy(); video.pause(); video.removeAttribute('src'); video.load() }
  }, [src, type, reduced])
  return <div className="background-video">
    <img src={poster} alt={label} className="video-poster" style={{ objectPosition: position }} />
    <video ref={ref} aria-hidden="true" muted loop playsInline preload="metadata" poster={poster} style={{ objectPosition: position, opacity: failed || reduced ? 0 : 1 }} onError={() => setFailed(true)} onPlay={() => setPlaying(true)} onPause={() => setPlaying(false)} />
    {!reduced && !failed && <button className="video-toggle" onClick={() => {
      paused.current = playing
      if (playing) ref.current?.pause()
      else void ref.current?.play().catch(() => setPlaying(false))
    }}>{playing ? 'Pause video' : 'Play video'}</button>}
  </div>
}
