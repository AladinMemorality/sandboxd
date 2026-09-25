# Fennec Meet scoped HTTP compatibility review

Read-only review identified two existing private HTTP dependencies for stable
Baarcha app `01M3C9C0TN9VTJ7MBCK9Z7BQV5`, sandbox
`01M3C9C0V0MQYNFTMCYS7CCNVC`. The separate shorter application identifier in its
operator deployment metadata is not the controller AppID. No gateway requests,
model calls, meeting creation/deletion, firewall changes or production
configuration changes were performed by this review.

The existing Docker exception covers two ports on one gateway. Cube must preserve
only the following application-scoped HTTP routes through the existing host
reverse channel; direct private TCP remains denied:

| Fixed origin | Method | Route |
|---|---|---|
| `http://10.40.14.68:8080` | GET | `/health` |
| `http://10.40.14.68:8080` | POST | `/api/transcribe`, `/api/chat` |
| `http://10.40.14.68:8321` | GET | `/health`, `/v1/bots/{uuid}/transcript` |
| `http://10.40.14.68:8321` | POST | `/v1/bots` |
| `http://10.40.14.68:8321` | DELETE | `/v1/bots/{uuid}`, `/v1/bots/{uuid}/data` |

`{uuid}` means exactly one complete lowercase hexadecimal 8-4-4-4-12 UUID segment.
It is not a regex, glob, prefix match or arbitrary placeholder. Extra segments,
uppercase/compact UUID aliases, encoded paths, traversal, queries (including an
empty `?`), absolute request targets and unconfigured methods remain denied.
Existing literal routes continue matching exactly. At most one typed segment per
route is allowed; method count is bounded to GET/POST/DELETE and total route count
remains32. DELETE is forwarded only when explicitly listed for that app/service
and the requested route matches. It is not added to model/bridge callback policy.

A reviewed operator configuration can use the following structure. It remains
inert: this review does not write `SANDBOXD_CUBE_APP_HTTP_SERVICES` or a firewall.
Merge with other individually reviewed apps, never replace their existing map.

```json
{
  "01M3C9C0TN9VTJ7MBCK9Z7BQV5": [
    {
      "origin": "http://10.40.14.68:8080",
      "routes": {"GET": ["/health"], "POST": ["/api/transcribe", "/api/chat"]}
    },
    {
      "origin": "http://10.40.14.68:8321",
      "routes": {
        "GET": ["/health", "/v1/bots/{uuid}/transcript"],
        "POST": ["/v1/bots"],
        "DELETE": ["/v1/bots/{uuid}", "/v1/bots/{uuid}/data"]
      }
    }
  ]
}
```

Both online and offline migration brokers already select services from the exact
persisted source AppID and runtime generation. Sibling apps and remixes receive
no mapping. Guest-provided app Authorization reaches only the configured origin;
no host credential, platform cookie, API token or arbitrary forwarded header is
injected. URL/protected-prefix checks, no DNS or proxy discovery, no redirect
following,16MiB request/response bounds,120-second request lifetime and immediate
stream flushing are unchanged. Chat uses SSE; the app polls the meeting adapter
with JSON. No app-side WebSocket is needed. Remote browser/CDP connections belong
to the separately operated meeting backend and are not new Cube permissions.

The app's owner-specific meeting data is a workspace sibling explicitly selected
for preservation by its private owner-home manifest. At observation two records
were completed; this is not a freeze or proof that the remote backend stays idle.
Before cutover, stop admitting uploads/meeting/AI requests, drain in-flight
transcription/AI and active remote meeting work, then preserve the complete app
and selected home/history/config generation. Retain existing encrypted app
credentials; do not copy shared platform credentials into a guest. The remote
backend's own data and deployment remain outside the app runtime migration.

Live source changed during review while coding-task count remained zero. The
later observed server imports the meeting AI module and requires `/api/chat`.
A final frozen source/config inventory is therefore mandatory; an idle coding
queue does not prevent manual application deployment or ordinary app writes.
The generic fleet preflight's preliminary pass does not certify these external
service dependencies. Actual authenticated health and approved synthetic
functional acceptance after migration remain separate rollout gates.
