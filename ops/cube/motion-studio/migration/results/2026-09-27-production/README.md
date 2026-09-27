# Motion production migration and browser acceptance

Motion now runs in Cube with its stable project identity and existing seven-film
worker library. The verified fleet is 71 Cube bindings (70 customer projects and
one owned fixture), two Docker projects, and 70 complete migration journals.
The global Cube default and replacement of the sandboxd controller remain pending.

The native import and verification committed runtime `22bedb034cef4844adc45ba5096dc297`
on template `tpl-c0c9813b42db46898f7ddd9f`. The first coordinator held after commit
on a transient worker connection. A recorded continuation inherited the same four
lock descriptions and retained the nested lock holder, verified the exact committed
journal, and completed controller activation without replaying migration. Seven
film JSON records matched the retained acceptance baseline. The original Docker
source and all migration archives remain retained.

Actual Chrome acceptance found two failures beyond the sequential API check:

- Seven simultaneous gallery posters exceeded the four-worker-request limit.
  Commit `b9e74a8` adds a bounded queue of 16 requests for at most five seconds,
  preserving four active requests and rechecking revocation after waiting.
- Browser cancellation of a video stream produced an unhandled AbortError in
  the customer's proxy and crashed its process. The included patch uses awaited
  stream pipelines, allowing cancellation to close the upstream without killing
  the server. `check-proxy-cancellation.mjs SOURCE crash|healthy` reproduces the
  original crash and verifies the repaired behavior with a synthetic local worker.

The normal file API applied the proxy change only to the Cube workspace. Explicit
activation then rejected the original manifest's unsupported top-level `env` key.
Those environment requirement notes were retained as YAML comments. Stateless
validation proved the effective web command, port, health path, and workers were
identical; encrypted environment values were unchanged. Activation subsequently
succeeded in the same Cube runtime. No worker restart or paid job was performed.
The application actor's final source check initially used a root-owned-artifact
reader on a customer workspace and refused; a separate read-only source check
verified the stopped original container, stable file identity, and original hash.
Neither activation nor migration was replayed to repair this check.

Validation: six Python coordinator tests; egress race suite; 366 API test/subtest
passes plus the remaining access-log configuration test rerun after supplying its
missing repository fixture. The earlier slow disposable Go runs were stopped and
replaced with tests using an executable tmpfs for their temporary databases.
The app's 24 tests, production build, and original/repaired cancellation fixtures
passed on Node 22. The image release verified nine simultaneous production-preview
requests (seven posters, status, and projects) before reopening. Independent
read-only verification reacquired all four locks, checked full online routes,
controller readiness, fleet counts, and the retained original source.

Final actual Chrome: Motion at 09:47:07–09:47:10 UTC, seven loaded posters, seven
projects, successful status/projects APIs, zero opening overlays and zero JavaScript
errors. Avocall at 09:36:04–09:36:09 UTC, successful wake, 5,302 body characters,
zero overlays/errors. Browser processes closed. See `result.json` for sanitized
receipts and hashes; full operator journals and customer screenshots remain private.

Current controller: `acb3e438285f19c51ea7155ff968ac3c8711471194fab73f95e417411a1edb0a`.
Image: `sha256:73e4f38a9a4373f861dd2648ce09a0ea79551681d1fbf31da931bd7da8457295`.
Both prior controller generations and their prepared plans are stale. New plans
must include the Motion template, read-only worker socket, and current controller
pin. The two remaining Minecraft projects still need their existing Playit tunnels.
