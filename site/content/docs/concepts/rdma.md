---
title: "RDMA"
date: 2025-05-25T11:20:46Z
---

## Understanding RDMA Components in Linux

RDMA (Remote Direct Memory Access) is a powerful technology enabling applications to directly read from or write to memory on a remote machine without involving the CPU, caches, or operating system of either machine during the data transfer. This achieves ultra-low latency and high throughput, making it ideal for high-performance computing (HPC), AI/ML, and storage.

In a Linux system, the RDMA ecosystem involves several interconnected components:

1. **RDMA Device (Host Channel Adapter - HCA)**

   * **What it is:** This is the physical hardware adapter card (like a Mellanox ConnectX series NIC) installed in a server. It's the "engine" that performs the RDMA operations. It contains specialized circuitry to offload networking protocols and manage direct memory access.  
   * **Linux Representation:** The Linux kernel's RDMA subsystem identifies these as "RDMA devices," often named like mlx5_0, mlx4_0, or hfi1_0. These names represent the underlying hardware abstraction.  
   * **Function:** Handles kernel bypass, zero-copy data movement, and protocol offload.

2. **/dev/infiniband/uverbsN (User Verbs Device)**  
   * **What it is:** These are special character device files (e.g., /dev/infiniband/uverbs0, /dev/infiniband/uverbs1) exposed by the ib_uverbs kernel module. They act as the **user-space interface** to the RDMA device (HCA).  
   * **Function:**  
     * **Control Path:** User applications (typically via the libibverbs library) interact with these files to set up and manage RDMA resources (e.g., allocating protection domains, registering memory regions, creating queue pairs for sending/receiving). These operations involve the kernel.  
     * **Memory Pinning:** Crucially, they facilitate "memory pinning" by telling the kernel to lock application memory pages in physical RAM, ensuring the HCA has stable addresses for direct access.  
     * **Enables Kernel Bypass:** While the control path goes through the kernel via these devices, the actual high-speed **data path** bypasses the kernel entirely once resources are set up, allowing the HCA to move data directly between memory buffers.

3. **RDMA Network Device (Standard Linux Network Interface)**  
   * **What it is:** This refers to the standard Linux network interface that is **associated with** or **built on top of** the RDMA device (HCA). It's the interface that receives an IP address and participates in the conventional TCP/IP networking stack.  
   * **Linux Representation:**  
     * For **InfiniBand (IB)**, this is typically an ibX interface (e.g., ib0) created via **IP over InfiniBand (IPoIB)**, allowing IP packets to be transported over the IB fabric.  
     * For **RDMA over Converged Ethernet (RoCE)** or **iWARP**, this is often the existing Ethernet interface name corresponding to the HCA (e.g., enp1s0f0, eth0).  
   * **Function:** Enables standard IP-based communication (ping, SSH, regular TCP/UDP applications) over the RDMA-capable hardware, serving as the familiar networking endpoint.

4. **RDMA Link Device (Refers to rdma link show output)**  
   * **What it is:** When you run rdma link show, the output describes a "link" that connects an RDMA device (HCA) to its associated standard network device. It essentially shows the binding or relationship between the low-level RDMA hardware abstraction and the higher-level network interface.  
   * **Linux Representation:** The rdma command, part of the rdma-core utilities, provides this specific view. For example, `link mlx5_0/1 state ACTIVE physical_state LINK_UP netdev ib0` shows that the RDMA device mlx5_0 (port 1) is active and linked to the standard network device ib0.  
   * **Function:** It provides a consolidated view of the RDMA device's status and its corresponding network interface. It's not a separate device type itself, but a representation of the relationship.

### Interplay and Why Network Connectivity is Required
 
* **Initialization and Control:** The /dev/infiniband/uverbsN devices are used for the **control plane** – setting up the RDMA connection, registering memory, and managing queues. This initial setup might involve the kernel and sometimes the associated network device (e.g., resolving IP addresses).  
* **Data Transport:** Once the RDMA connection is established via the control plane, the **data plane** takes over. The RDMA device (HCA) directly transfers data across the **network fabric** (InfiniBand or Ethernet) to the remote HCA, bypassing the CPU and OS for the actual data movement.  
* **Remote Access:** The "Remote" aspect of RDMA inherently requires a physical network (cables, switches) to connect the two machines. Without this network connectivity, there's no path for data to travel between "remote" memories.  
* **Addressing and Communication:** While RDMA data transfers are "offloaded," the endpoints still need to be identifiable (IP addresses for RoCE/iWARP, GIDs for InfiniBand) and initial connection setup requires communication over the network.

**In summary:**

The **RDMA Device (HCA)** is the specialized hardware. The **/dev/infiniband/uverbsN** devices are the Linux kernel's user-space interface to control that hardware. The **RDMA Network Device** is the standard IP-addressable interface built on top of the HCA for general network communication. And the **RDMA Link Device** (as seen in rdma link show) describes the direct relationship between the RDMA device and its network interface. All these components work together, relying on a functional network fabric, to enable the high-performance, low-latency data transfers characteristic of RDMA.

