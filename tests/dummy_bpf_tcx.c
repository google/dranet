#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>

SEC("tcx/ingress")
int handle_ingress(struct __sk_buff *skb) {
    return BPF_OK;
}

char LICENSE[] SEC("license") = "GPL";