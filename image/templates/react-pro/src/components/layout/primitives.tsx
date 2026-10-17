import type { HTMLAttributes, PropsWithChildren } from 'react'
import { cn } from '@/lib/utils'
export function Container({ className, ...props }: HTMLAttributes<HTMLDivElement>) { return <div className={cn('container', className)} {...props} /> }
export function Section({ className, ...props }: HTMLAttributes<HTMLElement>) { return <section className={cn('section', className)} {...props} /> }
export function Stack({ className, ...props }: HTMLAttributes<HTMLDivElement>) { return <div className={cn('flex flex-col gap-6', className)} {...props} /> }
export function Grid({ className, ...props }: HTMLAttributes<HTMLDivElement>) { return <div className={cn('grid gap-6 md:grid-cols-2', className)} {...props} /> }
export function VisuallyHidden({ children }: PropsWithChildren) { return <span className="sr-only">{children}</span> }
