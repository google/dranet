---
title: "RDMA Device Handling"
date: 2025-05-25T11:20:46Z
---

DRANET provides robust support for Remote Direct Memory Access (RDMA) devices, essential for high-performance computing (HPC) and AI/ML workloads that require ultra-low latency communication. DRANET's RDMA implementation intelligently handles device allocation based on the host system's RDMA network namespace mode.

### RDMA Device Handling in DRANET

DRANET manages three primary types of RDMA-related components for Pods:

1.  **RDMA Character Devices:** These are user-space interfaces (e.g., `/dev/infiniband/uverbsN`, `/dev/infiniband/rdma_cm`) that user applications interact with to set up RDMA resources.

2.  **RDMA Network Devices:** This refers to the standard Linux network interface (e.g., `eth0`, `gpu3rdma0`, `ib0`) that receives an IP address and participates in the conventional TCP/IP networking stack, often built on top of an HCA.

3.  **RDMA Link Devices:** This is the physical hardware adapter card (Host Channel Adapter - HCA), often named like `mlx5_0`, `mlx4_0`, or `hfi1_0`, which performs the core RDMA operations.

DRANET's behavior regarding the transfer of these devices to a Pod depends on the RDMA subsystem's network namespace mode on the host, which can be either "shared" or "exclusive". This mode is determined at DRANET startup.

#### Shared Mode (`rdma-system netns shared`)

In shared mode, the RDMA subsystem allows multiple network namespaces to access the same RDMA link devices.

* **Device Transfer:** DRANET will move both the **RDMA Character Devices** (like `/dev/infiniband/uverbsN` and `/dev/infiniband/rdma_cm`) and the **RDMA Network Device** (the standard network interface with its IP configuration) into the Pod's namespace. However, the **RDMA Link Device** itself (e.g., `mlx5_0`) remains in the host's root network namespace and is not moved to the Pod's namespace.

* **"Soft" Namespacing:** This mode provides a "soft" form of namespacing. While the Pod has its own character devices and dedicated network interface for RDMA operations, the underlying RDMA link device is still shared with the host and potentially other Pods. This means other processes or Pods on the host could theoretically still access or influence the RDMA link device.

#### Exclusive Mode (`rdma-system netns exclusive`)

In exclusive mode, the RDMA subsystem enforces stricter isolation, allowing an RDMA device to be assigned to only one network namespace at a time.

* **Device Transfer:** When DRANET operates in exclusive mode, it moves all three types of components: the **RDMA Character Devices**, the **RDMA Network Device**, and the **RDMA Link Device** (e.g., `mlx5_0`) entirely into the Pod's network namespace.
* **"Hard" Namespacing:** This provides full, "hard" namespacing for the RDMA device. Once moved, the RDMA link is no longer directly accessible from the host's root network namespace or other Pods (unless explicitly configured). This ensures strong isolation for the Pod's RDMA workloads.
