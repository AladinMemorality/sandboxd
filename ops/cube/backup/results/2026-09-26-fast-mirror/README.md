# Faster scope validation during mirror preparation

The completed production copy contains about1.94million entries across112
reviewed roots. Preparation repeatedly constructed every root and ancestor chain
for each entry, leaving the Python process CPU-bound despite NVMe storage.

The helper now caches canonical root strings and checks each file's ancestors
against that set. Exact ancestry, canonical path spelling, traversal refusal,
literal names and the complete preparation/sealing checks are preserved.
All20 native mirror tests passed with no skips, including the new112-root
boundary case and existing real rsync, archive, hardlink, metadata and corruption
tests.

The native1000-path scope-check benchmark took4.2246s before and0.0179s after:
approximately236× faster for that operation. This is not a whole-job speedup or
completion ETA. The local measurement was approximately240×.

The old private preparation helper was terminated by its verified process handle,
and its unaccepted partial index retained. The already-transferred file tree and
source manifests were preserved. A new private preparation job uses the tested
helper. No production services, customer source files or worker state changed.
Preparation still does not prove a frozen backup or authorize cache promotion.
