# Aborted-project retry acceptance — September 26, 2026

Two aborted customer migrations now completed: LevelUpia redesign
(`01M36Y40EZ5GS521R44RF2S770`) and Jaloul Ayed site
(`01M1XF6PYN6MH83F5YBBYBKYT2`). The production API verified HTML,
entry modules, three exact source files per project and two historical tasks.
Original Docker sources and first-attempt archives remain retained.

The explicit replan retained both full old journals/task scopes in the new
attempt audit table, with distinct archive generations for the new attempts.
The previously rejected 294,817,991-byte workspace archive imported and verified
successfully through the corrected Cube proxy. This is actual large-transfer
acceptance, beyond the earlier header-only probes.

The Jaloul retry removed an empty target-only `.cache` directory under the
strict source-absence/owner/mode checks, then passed owner-home verification.
This supports the template-scaffolding diagnosis; the original failed target
had already been deleted and was not retrospectively inspected.

Unit `cube-cohort-migrate-20260926-06.service`, invocation
`6f474af4168e4b44943b1b765ac703eb`, exited0. Maintenance lasted192.013seconds;
production reopened at20:59:18UTC. Independent checks reacquired all four locks,
compared full live routes and verified controller readiness and retained stopped
sources. Fleet:30Cube (29customer+fixture),43Docker;29complete journals,
zero aborted/incomplete current journals; old attempts preserved separately.
Tasks remain42failed/123succeeded; no AI tasks or worker power operations.

Runtime source4634177; native CLI SHA
`ba177c541cd35685b38ca5194258b8b12a6ad08aef905bbcea9b5a4e1020055b`.
Native race tests for CLI/migration/store passed in the preceding build.
Native38cutover+23maintenance+6planned tests passed before this execution.
The new-project default and controller replacement remain incomplete.
