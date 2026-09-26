# Live Motion guest/worker acceptance

The second owned fixture passed against production on September 26, 2026.
Production reopened at 23:49:27 UTC after 106.87 seconds of maintenance.
The controller was ready, its complete online route tree matched the retained
baseline, and all four operator locks were independently reacquired afterward.

Two fresh guests used `tpl-c0c9813b42db46898f7ddd9f`, canonical four-slot admission
and separate private fixture journals. Both authenticated supervisors advertised
`motion-worker-v1`. The exact existing customer's frontend proxy, pinned by its
hash in `result.json`, ran in each guest. Through the authorized guest's private
application ingress, the actual worker accepted a synthetic film, PATCH and a
52,428,800-byte valid MP4. A 52,428,801-byte upload returned 413 without changing
the film. HEAD, prefix/suffix ranges and full download hash passed; ffmpeg
decoded a video frame from the downloaded file.

The sibling received 502 because its broker had no named Motion service. The
owner's stale journal generation received 403; reattachment with the current
generation restored access, and detachment revoked it again. No paid media jobs
were submitted. All seven existing worker project JSON documents were unchanged.
Both guest deletions were independently confirmed by provider 404 and released
admission. The synthetic film was removed. Neither fixture app was inserted in
the canonical customer app table, and all non-admission/storage canonical tables
matched their fenced baseline.

The first attempt lasted 37.00 seconds and failed during guest setup. It created
one owned guest and no film; cleanup and independent production restoration
passed. Source inspection found the harness racing the asynchronous supervisor
restart after import. The successful second runner waits for authenticated boot
and config-revision changes, with race-tested regression coverage.

The executed source hashes are retained in `result.json`. A subsequent
cleanup-only hardening adds refusal when an interrupted worker request has no
complete HTTP acknowledgement, because a worker-side upload tail can outlive
the guest channel. Seven Node tests and three Python tests cover the final
harness, including that refusal; the successful live run already had the
required complete HTTP receipt. Two Go sequencing tests passed under `-race`.
No second live run was needed for that additional failure-only refusal.

This establishes actual Motion guest/worker compatibility. Customer migration,
controller socket mount/app selection/template admission and the final
Cube-only controller/default deployment remain separate work. The customer
fleet is unchanged: 69 migrated customer projects, one older owned Cube fixture,
and three Docker customer projects. All 69 current customer migration journals
remain complete.
