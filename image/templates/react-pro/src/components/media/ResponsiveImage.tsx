import { useEffect, useState, type ImgHTMLAttributes, type ReactNode } from 'react'
export function ResponsiveImage({ src, alt, fallback, ...props }: ImgHTMLAttributes<HTMLImageElement> & { alt: string; fallback?: ReactNode }) {
  const [failed, setFailed] = useState(false)
  useEffect(() => setFailed(false), [src])
  return failed || !src ? <>{fallback ?? <div role="img" aria-label={alt} className="media-fallback">{alt}</div>}</> : <img src={src} alt={alt} loading="lazy" decoding="async" {...props} onError={event => { setFailed(true); props.onError?.(event) }} />
}
