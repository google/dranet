# Running integration tests


1. Install `bats` https://bats-core.readthedocs.io/en/stable/installation.html

2. Install `kind` https://kind.sigs.k8s.io/

3. Ensure git submodules have been initialized: `git submodule update --init --recursive`

4. Run `bats tests/`

# Best practices for writing integration tests

* For clear and debuggable test failures, prefer using a suitable helper
  assertion from https://github.com/bats-core/bats-assert. These functions
  automatically show "got" (actual) and "want" (expected) outputs, making it
  easier to write tests and pinpoint issues.

## eBPF programs

We store the compiled bytecode for simplicity, those are generated using `clang -O2 -g -target bpf -c dummy_bpf_tcx.c -o dummy_bpf_tcx.o`