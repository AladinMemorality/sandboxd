#!/usr/bin/env python3
"""Userspace tests of the exact candidate reply header; no BPF/network access."""
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent


def candidate_header():
    patch = (ROOT / "0002-track-inbound-replies.patch").read_text()
    marker = "+++ b/CubeNet/src/baarcha_reply.h\n"
    if patch.count(marker) != 1:
        raise ValueError("expected exactly one complete reply header")
    lines = patch.split(marker, 1)[1].splitlines()
    if any(not line.startswith(("@@", "+")) for line in lines):
        raise ValueError("reply header must remain a complete added file")
    return "\n".join(line[1:] for line in lines if line.startswith("+")) + "\n"


class ReplyPermissionTests(unittest.TestCase):
    def test_actual_header_with_fake_maps_and_clock(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "baarcha_reply.h").write_text(candidate_header())
            (root / "check.c").write_text(r'''#include <stdint.h>
#include <stdbool.h>
#include <assert.h>
#include <string.h>
typedef uint32_t __u32;
typedef uint16_t __u16;
typedef uint64_t __u64;
#ifndef __always_inline
#define __always_inline inline __attribute__((always_inline))
#endif
#define __uint(name, value) int name
#define __type(name, type) type *name
#define SEC(name)
#define BPF_ANY 0
struct mvm_meta { __u32 version; };
static int ifindex_to_mvmmeta;
static __u64 clock_ns = 1000000000ULL;
static void *bpf_map_lookup_elem(void *, const void *);
static int bpf_map_update_elem(void *, const void *, const void *, int);
static __u64 bpf_ktime_get_ns(void) { return clock_ns; }
#include "baarcha_reply.h"
static struct mvm_meta guests[2] = {{7}, {7}};
static struct baarcha_reply_key saved_key;
static struct baarcha_reply_value saved_value;
static bool occupied, fail_write;
static void *bpf_map_lookup_elem(void *map, const void *key) {
 if (map == &ifindex_to_mvmmeta) {
  __u32 id = *(const __u32 *)key;
  return id >= 10 && id <= 11 ? &guests[id-10] : 0;
 }
 assert(map == &baarcha_replies);
 return occupied && !memcmp(key, &saved_key, sizeof(saved_key)) ? &saved_value : 0;
}
static int bpf_map_update_elem(void *map, const void *key, const void *value, int flags) {
 assert(map == &baarcha_replies && flags == BPF_ANY);
 if (fail_write) return -1;
 memcpy(&saved_key, key, sizeof(saved_key));
 memcpy(&saved_value, value, sizeof(saved_value));
 occupied = true;
 return 0;
}
int main(void) {
 /* No permission from missing state, ACK or SYNACK. */
 assert(!baarcha_reply_allowed(10, 123, 45000, 3031));
 assert(!baarcha_track_reply(10, 123, 45000, 3031, false, true));
 assert(!baarcha_track_reply(10, 123, 45000, 3031, true, true));
 /* A real host-side ingress SYN grants only its complete tuple. */
 assert(baarcha_track_reply(10, 123, 45000, 3031, true, false));
 assert(baarcha_reply_allowed(10, 123, 45000, 3031));
 assert(baarcha_track_reply(10, 123, 45000, 3031, false, true));
 assert(!baarcha_reply_allowed(11, 123, 45000, 3031));
 assert(!baarcha_reply_allowed(10, 124, 45000, 3031));
 assert(!baarcha_reply_allowed(10, 123, 45001, 3031));
 assert(!baarcha_reply_allowed(10, 123, 45000, 3000));
 assert(!baarcha_reply_allowed(12, 123, 45000, 3031));
 guests[0].version++;
 assert(!baarcha_reply_allowed(10, 123, 45000, 3031));
 assert(baarcha_track_reply(10, 123, 45000, 3031, true, false));
 clock_ns += BAARCHA_REPLY_IDLE_NS + 1;
 assert(!baarcha_reply_allowed(10, 123, 45000, 3031));
 /* Fail closed if worker state is unavailable or permission cannot persist. */
 assert(!baarcha_track_reply(12, 123, 45000, 3031, true, false));
 occupied = false;
 fail_write = true;
 assert(!baarcha_track_reply(10, 123, 45000, 3031, true, false));
 assert(!baarcha_reply_allowed(10, 123, 45000, 3031));
 /* Characterization: accepted outbound replies refresh the idle clock. */
 fail_write = false;
 assert(baarcha_track_reply(10, 123, 45000, 3031, true, false));
 clock_ns += BAARCHA_REPLY_IDLE_NS - 1;
 assert(baarcha_reply_allowed(10, 123, 45000, 3031));
 clock_ns += BAARCHA_REPLY_IDLE_NS - 1;
 assert(baarcha_reply_allowed(10, 123, 45000, 3031));
 return 0;
}
''')
            subprocess.run(["cc", "-std=c11", "-Wall", "-Wextra", "-Werror", "-I", str(root),
                            str(root / "check.c"), "-o", str(root / "check")], check=True)
            subprocess.run([str(root / "check")], check=True, timeout=5)


if __name__ == "__main__":
    unittest.main()
