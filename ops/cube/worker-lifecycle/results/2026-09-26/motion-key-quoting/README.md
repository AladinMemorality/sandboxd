# Quoted EnvironmentFile key compatibility

Read-only preflight found a reviewed quoted Motion key in worker.env. The narrow
parser accepts unquoted or matching single/double-quoted printable tokens without
shell evaluation. Unsupported escapes, continuations, control characters,
ambiguous quotes and duplicate keys refuse. Exactly the same parsed bytes are
compared to the live process key and used for the authenticated read-only request.

All96 Linux-root tests passed; regression also exercises the real motion_jobs
method with a quoted fixture key and verifies the outbound authentication header.
No production credential values or live mutations are included in the test data.
Maintenance SHA:
`33f40ad83a6773f7f3941c3ba36f1aa38bce821d802b2d37b1eec349c97613cb`.
