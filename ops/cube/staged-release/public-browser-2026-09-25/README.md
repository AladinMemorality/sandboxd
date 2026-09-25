# Public app audit and scoped asset repair — 2026-09-25

The deployed platform was `30cd5c24ff44183fc72f4c051377f8ae931d9863` before and after the audit. All **20 public apps** rendered actual iframe content and cleared their loading indicator, with zero page JavaScript errors. Three author-directory pages returned HTTP 200 and correctly had no app iframe. Kanzari was absent from the public sitemap and returned the expected anonymous 404; its last confirmed visibility was private.

The browser used ordinary anonymous navigation, at most two pages concurrently, and at least five seconds between starts. No forms were submitted or model calls initiated by the harness. Initial render took 5.3–21.5 seconds (median 8.0 seconds) across the external network. These are end-to-end WAN observations, **not Cube performance measurements**, and do not establish complete business-flow or owner-only control correctness.

Tunde Bridge rendered but requested one image and one video from `https://localhost:3100`. A second browser visit reproduced both SSL failures. Read-only database and runtime checks established that both files were undeleted ordinary public-capability uploads belonging to the app owner, and that the current app/container binding matched. No private screenshot was exposed.

The authorized repair changed exactly two URLs in `src/lib/brand.ts` and corrected one misleading developer note in `BRAIN.md`. Original inode, mode, ownership and SHA-256 were checked; private original backups were fsynced before atomic replacement. Active task state and app/container identity were checked before and after. No other links, visibility, cover or snapshots changed. The app had no published/runtime snapshot and remix was disabled; the published iframe uses the current sandbox, so republishing was unnecessary.

A fresh anonymous browser then confirmed the canonical image response was **200**, the video response **206**, all four image elements loaded, and the 1024×576 video reached readyState 4. There were no remaining localhost references, failed resource requests or page JavaScript errors. The browser was closed. The original files and exact private repair receipt remain in operator storage; their hashes and sanitized outcomes are in `results.json`.

This evidence predates deployment of the later merged CI-fix revision `11981fd9cdbf0f33b82fbbcd467783176a113fde`. That release is tracked separately and was not falsely attributed to this audit.
