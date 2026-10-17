import { motion, useReducedMotion, type HTMLMotionProps } from 'motion/react'
export function Reveal({ children, duration = .6, offset = 18, ...props }: HTMLMotionProps<'div'> & { duration?: number; offset?: number }) {
  const reduced = useReducedMotion()
  return <motion.div initial={reduced ? false : { opacity: 0, y: offset }} whileInView={{ opacity: 1, y: 0 }} viewport={{ once: true, amount: .1 }} transition={{ duration: reduced ? 0 : duration, ease: [.22, 1, .36, 1] }} {...props}>{children}</motion.div>
}
export function Stagger({ children, delay = .08, ...props }: HTMLMotionProps<'div'> & { delay?: number }) {
  const reduced = useReducedMotion()
  return <motion.div initial="hidden" whileInView="visible" viewport={{ once: true }} variants={{ hidden: {}, visible: { transition: { staggerChildren: reduced ? 0 : delay } } }} {...props}>{children}</motion.div>
}
export function StaggerItem({ children, ...props }: HTMLMotionProps<'div'>) {
  const reduced = useReducedMotion()
  return <motion.div variants={{ hidden: { opacity: reduced ? 1 : 0, y: reduced ? 0 : 12 }, visible: { opacity: 1, y: 0 } }} {...props}>{children}</motion.div>
}
