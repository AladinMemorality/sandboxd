import { useState } from 'react'
import { Link, Routes, Route } from 'react-router-dom'
import { ArrowUpRight, Check } from 'lucide-react'
import { Shell, NotFound } from '@/components/layout/Shell'
import { Reveal } from '@/components/motion/Reveal'
import { FAQ } from '@/sections/FAQ'
import Contact from '@/pages/Contact'
import { site } from '@/content/site'
function Home() {
  return <div className="container"><section className="hero"><Reveal><p className="eyebrow">{site.eyebrow}</p><h1>{site.headline}</h1><p className="lede">{site.description}</p><Link to="/pricing" className="action">Find your space <ArrowUpRight size={18} /></Link></Reveal><div className="editorial-art" aria-hidden="true"><div className="art-orbit" /><div className="art-disc" /><span>Less noise.<br />More possibility.</span><small>01 / A fresh perspective</small></div></section>
    <section className="section statement"><p className="eyebrow">Thoughtfully simple</p><h2>A clear view.<br />A little momentum.<br /><span>Something worth making.</span></h2><p className="lede">Keep the essentials close. Let the rest fall away. Find a way of working that feels like your own.</p></section><FAQ /></div>
}
function Pricing() {
  const [yearly, setYearly] = useState(false)
  return <div className="container section"><p className="eyebrow">Simple plans</p><h1>A space for every stage.</h1><p className="lede">Choose what fits your way of working.</p><div className="billing-toggle" role="group" aria-label="Billing interval"><button aria-pressed={!yearly} onClick={() => setYearly(false)}>Monthly</button><button aria-pressed={yearly} onClick={() => setYearly(true)}>Yearly</button></div>
    <div className="pricing-grid">{site.plans.map(plan => <article key={plan.name} className="plan"><p className="eyebrow">{plan.name}</p><h2>${yearly ? plan.yearly : plan.monthly}<small> / month</small></h2><p>{plan.description}</p><p className="fine-print">{yearly ? `$${plan.yearly * 12} billed yearly` : 'Billed monthly'}</p><ul>{plan.features.map(feature => <li key={feature}><Check size={16} />{feature}</li>)}</ul><Link className="action" to={`/contact?plan=${encodeURIComponent(plan.name)}`}>Ask about {plan.name}<ArrowUpRight size={16} /></Link></article>)}</div><FAQ /></div>
}
export default function Marketing() { return <Shell links={[{ to: '/', label: 'Overview' }, { to: '/pricing', label: 'Pricing' }, { to: '/contact', label: 'Let’s talk' }]}><Routes><Route path="/" element={<Home />} /><Route path="/pricing" element={<Pricing />} /><Route path="/contact" element={<Contact />} /><Route path="*" element={<NotFound />} /></Routes></Shell> }
