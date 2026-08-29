package wake

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// The wake pages carry the platform brand (Baarcha / Hannibal) but no
// sandbox ids, internal reason codes, or host jargon in the body. The
// machine-readable reason still travels in response headers
// (X-Wake-Error / X-Retry-After-Reason) for log correlation. CSS `%`
// literals are kept out of fmt — the templates use placeholder tokens
// and strings.NewReplacer.

// homeURL is where the banner's "Create your own" points: the consumer
// site that created these sandboxes. Empty hides the link (the brand
// line stays), so an unconfigured install degrades gracefully.
var homeURL = os.Getenv("SANDBOXD_BRAND_HOME")

func fill(tmpl string, pairs ...string) string {
	link := ""
	if homeURL != "" {
		link = `<a class="cta" href="` + homeURL + `">Create your own</a>`
	}
	pairs = append(pairs, "@CTA@", link)
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// writeRefreshPage emits the waking page with a meta-refresh. By the
// time the browser refreshes, Traefik's Docker provider has typically
// observed the `docker start` and the dynamic per-host route
// (priority 100) wins over the catch-all (priority 1), so the second
// request hits the live container directly.
func writeRefreshPage(w http.ResponseWriter, status int, id string, refreshSec int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, fill(refreshTmpl, "@REFRESH@", strconv.Itoa(refreshSec)))
}

// writeBusyPage is the 503-shape page used when admission denies a
// wake. The Retry-After contract is unchanged; the body self-refreshes
// after the retry window. `reason` travels only in the header.
func writeBusyPage(w http.ResponseWriter, id, reason string, retryAfter int, availPct float64) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	w.Header().Set("X-Retry-After-Reason", reason)
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = io.WriteString(w, fill(busyTmpl, "@RETRY@", strconv.Itoa(retryAfter)))
}

// writeErrorPage is the 503 page used when wake failed for a non-
// admission reason (start failed, tcp-ready timeout, not_found, …).
// X-Wake-Error carries the precise reason.
func writeErrorPage(w http.ResponseWriter, id, reason string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Wake-Error", reason)
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = io.WriteString(w, fill(errorTmpl))
}

// --- shared style -----------------------------------------------------
// One const so the three pages stay a visual family — and the same
// family as the sandbox waiting screen and the Baarcha site: warm
// cream, serif display, pomegranate accent.
const pageCSS = `
  *{box-sizing:border-box}
  html,body{height:100%}
  body{margin:0;display:flex;flex-direction:column;
       font-family:"Helvetica Neue",-apple-system,"Segoe UI",Arial,sans-serif;
       background:#f4efe6;color:#23201b}
  .bar{display:flex;align-items:center;justify-content:space-between;gap:16px;
       padding:14px 24px;border-bottom:1px solid rgba(35,32,27,.14);background:#f2ede3}
  .brand{font-family:Georgia,"Times New Roman",serif;font-weight:700;font-size:17px;
       letter-spacing:.02em;color:#23201b;text-decoration:none}
  .made{color:#6d675e;font-size:12px;letter-spacing:.08em;text-transform:uppercase}
  .cta{color:#a3372e;font-size:12px;font-weight:700;letter-spacing:.08em;
       text-transform:uppercase;text-decoration:none}
  main{flex:1;display:grid;place-content:center;justify-items:center;
       padding:32px;text-align:center}
  .kicker{display:flex;align-items:center;gap:8px;margin:0 0 12px;color:#a3372e;
       font-size:13px;font-weight:700;letter-spacing:.14em;text-transform:uppercase}
  h1{margin:0;font-family:Georgia,"Times New Roman",serif;font-weight:600;
       font-size:clamp(28px,5vw,42px);line-height:1.15;letter-spacing:-.01em}
  p{max-width:420px;margin:14px 0 0;color:#6d675e;font-size:16px;line-height:1.6}
  .pulse{width:8px;height:8px;border-radius:50%;background:#a3372e;
       animation:pulse 1.6s ease-in-out infinite}
  @keyframes pulse{0%,100%{opacity:1}50%{opacity:.35}}
  button{margin-top:22px;font:inherit;font-size:15px;font-weight:600;color:#fff;
       cursor:pointer;border:1px solid #a3372e;border-radius:999px;
       padding:10px 22px;background:#a3372e}
  button:active{transform:translateY(1px)}
  @media(prefers-reduced-motion:reduce){.pulse{animation:none}}
`

const banner = `
  <header class="bar">
    <span class="brand">Baarcha</span>
    <span class="made">Made with Hannibal</span>
    @CTA@
  </header>`

// --- the three pages --------------------------------------------------

const refreshTmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Waking your app · Baarcha</title>
<meta http-equiv="refresh" content="@REFRESH@">
<style>` + pageCSS + `</style>
</head>
<body>` + banner + `
  <main>
    <p class="kicker"><span class="pulse"></span> Baarcha sandbox</p>
    <h1>Waking your app.</h1>
    <p>It was asleep to save your machine. A second or two and it is live.</p>
  </main>
</body>
</html>
`

const busyTmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>One moment · Baarcha</title>
<meta http-equiv="refresh" content="@RETRY@">
<style>` + pageCSS + `</style>
</head>
<body>` + banner + `
  <main>
    <p class="kicker"><span class="pulse"></span> Baarcha sandbox</p>
    <h1>One moment.</h1>
    <p>A lot is happening right now. Your app comes back in a few seconds on its own.</p>
  </main>
</body>
</html>
`

const errorTmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>It did not come up · Baarcha</title>
<style>` + pageCSS + `</style>
</head>
<body>` + banner + `
  <main>
    <p class="kicker">Baarcha sandbox</p>
    <h1>It did not come up.</h1>
    <p>Something hiccuped while starting your app. Give it another try in a moment.</p>
    <button onclick="location.reload()">Try again</button>
  </main>
</body>
</html>
`
