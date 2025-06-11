#include <linux/bpf.h>
#include <linux/pkt_cls.h>

__attribute__((section("classifier"), used))
int handle_ingress(struct __sk_buff *skb) {
    return TC_ACT_OK;
}

char __license[] __attribute__((section("license"), used)) = "GPL";