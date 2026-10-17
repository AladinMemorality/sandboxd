import { site } from '@/content/site'
export function FAQ() {
  return <section className="section faq"><div><p className="eyebrow">Good to know</p><h2>A few answers.</h2></div><div>{site.questions.map(item => <details key={item.question}><summary>{item.question}</summary><p>{item.answer}</p></details>)}</div></section>
}
