#!/usr/bin/env python3
"""Compile and exercise the actual generated C predicate; no network needed.

This is a userspace predicate test, NOT a BPF verifier or packet-path test.
"""
import pathlib
import subprocess
import tempfile
import unittest
from render_hard_deny import render


class HardDenyTests(unittest.TestCase):
    def test_operator_validation(self):
        for management, resolvers in [([], []), (["::/0"], []),
                                      (["0.0.0.0/0"], []),
                                      (["203.0.113.7/32"], ["203.0.113.7"]),
                                      (["203.0.113.7/32"], ["::1"]),
                                      (["203.0.113.7/24"], [])]:
            with self.assertRaises(ValueError):
                render(management, resolvers)

    def test_generated_c_predicate(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "bpf").mkdir()
            (root / "bpf/bpf_endian.h").write_text(
                '#include <arpa/inet.h>\n#define bpf_ntohl(x) ntohl(x)\n#define bpf_htons(x) htons(x)\n')
            (root / "baarcha_hard_deny.h").write_text(render(["203.0.113.7/32"], ["10.0.0.53"]))
            (root / "check.c").write_text('''
#include <stdint.h>
#include <stdbool.h>
#include <assert.h>
typedef uint32_t __u32;
typedef uint16_t __u16;
typedef uint8_t __u8;
#ifndef __always_inline
#define __always_inline inline __attribute__((always_inline))
#endif
#include "baarcha_hard_deny.h"
int main(void) {
 const char *blocked[] = {"0.1.2.3", "10.255.255.255", "100.64.0.1",
   "100.127.255.255", "127.0.0.1", "169.254.169.254", "172.16.0.1",
   "172.31.255.255", "192.168.255.254", "198.18.0.1", "224.0.0.1",
   "255.255.255.255", "203.0.113.7"};
 for (unsigned i=0; i<sizeof(blocked)/sizeof(blocked[0]); i++) {
   __u32 ip=inet_addr(blocked[i]);
   assert(baarcha_hard_deny_ip(ip));
   assert(baarcha_hard_deny_flow(ip,htons(443),6));
   assert(baarcha_hard_deny_flow(ip,htons(443),17));
   assert(baarcha_hard_deny_flow(ip,0,1));
 }
 const char *public[] = {"1.1.1.1","8.8.8.8","100.128.0.1","172.32.0.1","203.0.113.8"};
 for (unsigned i=0;i<sizeof(public)/sizeof(public[0]);i++)
   assert(!baarcha_hard_deny_ip(inet_addr(public[i])));
 __u32 resolver=inet_addr("10.0.0.53");
 assert(baarcha_hard_deny_ip(resolver)); /* never learn as general allow IP */
 assert(!baarcha_hard_deny_flow(resolver,htons(53),17));
 assert(baarcha_hard_deny_flow(resolver,htons(53),6));
 assert(baarcha_hard_deny_flow(resolver,htons(80),17));
 assert(baarcha_hard_deny_flow(inet_addr("10.0.0.54"),htons(53),17));
 return 0;
}
''')
            subprocess.run(["cc", "-std=c11", "-Wall", "-Werror", "-I", str(root),
                            str(root / "check.c"), "-o", str(root / "check")], check=True)
            subprocess.run([str(root / "check")], check=True)


if __name__ == "__main__":
    unittest.main()
