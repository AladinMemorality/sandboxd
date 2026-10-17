import type { PropsWithChildren } from 'react'
export function MediaFrame({ children, ratio = '16 / 9', className = '' }: PropsWithChildren<{ ratio?: string; className?: string }>) {
  return <div className={`media-frame ${className}`} style={{ aspectRatio: ratio }}>{children}</div>
}
