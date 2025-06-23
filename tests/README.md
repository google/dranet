# Integration tests


1. Install `bats` https://bats-core.readthedocs.io/en/stable/installation.html

2. Install `kind` https://kind.sigs.k8s.io/

3. Run `bats tests/`


## eBPF programs

We store the compiled bytecode for simplicity, those are generated using `clang -O2 -g -target bpf -c dummy_bpf_tcx.c -o dummy_bpf_tcx.o`