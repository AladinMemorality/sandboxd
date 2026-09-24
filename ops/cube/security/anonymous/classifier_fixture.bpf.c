/* Test-only wrapper: production session.h and generated hard-deny policy.
 * No attachments or transmission. Input/output is a 24-byte synthetic buffer.
 */
#include <vmlinux.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_endian.h>
#include "session.h"
struct fixture_input {
    __u32 ifindex;
    __u32 destination;
    __u16 port;
    __u8 protocol;
    __u8 result;
    __u32 current_version;
    __u32 cached_version;
    __u32 reserved;
};
SEC("tc")
int fixture_classify(struct __sk_buff *skb)
{
    struct fixture_input in = {};
    if (bpf_skb_load_bytes(skb, 0, &in, sizeof(in)))
        return 2;
    in.result = classify_egress_flow(in.ifindex, in.destination, in.port, in.protocol);
    return bpf_skb_store_bytes(skb, 0, &in, sizeof(in), 0) ? 2 : 0;
}
SEC("tc")
int fixture_cached(struct __sk_buff *skb)
{
    struct fixture_input in = {};
    struct nat_session sess = {};
    if (bpf_skb_load_bytes(skb, 0, &in, sizeof(in)))
        return 2;
    sess.policy_version = in.cached_version;
    in.result = session_policy_revoked(&sess, in.current_version, in.ifindex,
                                     in.destination, in.port, in.protocol);
    return bpf_skb_store_bytes(skb, 0, &in, sizeof(in), 0) ? 2 : 0;
}
char __license[] SEC("license") = "Dual BSD/GPL";
