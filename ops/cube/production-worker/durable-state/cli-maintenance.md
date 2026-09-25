# Durable-mode legacy CLI safety (0011)

The installed upstream cubecli's `unsafe restoredb` targets the old volatile
State database and performs an uncoordinated file copy. Required patch0011 loads
the selected config first and rejects the command whenever
`durable_metadata_root` is configured. Rejection occurs before confirmation or
any filesystem copy. No new destructive restore behavior is provided.

The regression test invokes the actual CLI command with a durable config and a
sentinel current metadata file. It requires the explicit refusal and unchanged
sentinel, with no legacy root/State tree created. This is separate from0010 and
the Cubelet artifact: a newly built CLI must be explicitly installed for the
running operator command to gain this safeguard.

The install target is
`/usr/local/services/cubetoolbox/Cubelet/bin/cubecli`; `/usr/local/bin/cubecli`
resolves to it. Preserve/hash the existing binary before any reviewed atomic
replacement, install root-owned0755, and fsync the file/parent. Do not restart
Cubelet or modify its config to install this operator CLI. Do not test the old
unsafe restore command against real metadata; the regression uses isolated
synthetic directories only.

This narrow guard does not make the legacy `image fix`, `network ls` or
`unsafe volumedb` State-path assumptions compatible with the durable layout.
These maintenance commands remain unsupported. The capture workflow's
`cubecli inspect`/storage RPCs query the running service and do not use those
old direct-DB paths. Review complete offline recovery separately; no live copy
of a local six-hour backup may overwrite current acknowledged metadata.


The real CLI-action regression passed with the race detector (1.035s), and the
complete native CLI compiled. Candidate:
`/root/cube-production/durable-cli-candidate/cubecli-candidate`, SHA256
`24fa1fc4846efe74084bf9b27d8c5ce2b0a952f0ad1bb107b25091354fa1d05c`.
No CLI was installed by the build. Source module files remained unchanged;
missing already-pinned CLI-only dependencies were cached before the final
network-isolated bounded build. Evidence is in
[cli-correction](results/2026-09-25/cli-correction/summary.json).
