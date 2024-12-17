# DRANET: DRA Kubernetes Network Driver

DRANET is a Kubernetes Network Driver that uses Dynamic Resource Allocation (DRA) to deliver high-performance networking for demanding applications in Kubernetes.

## Key Features

- **DRA Integration:** Leverages the power of Kubernetes' Dynamic Resource Allocation.
- **High-Performance Networking:** Designed for demanding workloads like AI/ML applications.
- **Simplified Management:** Easy to deploy and manage.
- **Enhanced Efficiency:** Optimizes resource utilization for improved overall performance.
- **Cluster-Wide Scalability:**  Effectively manages network resources across a large number of nodes for seamless operation in Kubernetes deployments.

## How It Works

The networking DRA driver uses GRPC to communicate with the Kubelet via the [DRA API](https://github.com/kubernetes/kubernetes/blob/3bec2450efd29787df0f27415de4e8049979654f/staging/src/k8s.io/kubelet/pkg/apis/dra/v1beta1/api.proto) and the Container Runtime via [NRI](https://github.com/containerd/nri). This architecture facilitates the supportability and reduces the complexity of the solution, it also makes it fully compatible and agnostic of the existing CNI plugins in the cluster.

The DRA driver, once the Pod network namespaces has been created, will receive a GRPC call from the Container Runtime via NRI to execute the corresponding configuration. A more detailed diagram can be found in:

[![](https://mermaid.ink/img/pako:eNp9UstuwyAQ_JUVp1ZNfoBDpMi-WFXdyLn6gs0mQTXgLtCHovx714nTWoobDgiW2dlhNEfReo1CioDvCV2LuVF7UrZ2wEul6F2yDdLl_pwa7DAul6vVU4nx09Mb5NUacjIfSBJK5toQ9oqwwuATtRgeHi-9pY8InmEw1_naRGUcxAPCtTPrlLF8Y10hgnIaMu92Zj_S3ZAMqpajwvtSrt_gXzDlMBhJS6iS23i95UmN_7pi_wADf1YWEniDdZ6P72VxfpjwMEmxCXPts55VBRy8f5sff981xoMb605ZDL1qGd4jqWi8C_esmiqGG7FTK2eF_eNhRqgi_lbCjI1T6lu4WAiLZJXRHMrj0FwLToXFWkg-atyp1MVa1O7E0CGg22_XChkp4UKkXjPfmGEhd6oLXEVtoqeXS9DPeT_9ABUC_8M?type=png)](https://mermaid.live/edit#pako:eNp9UstuwyAQ_JUVp1ZNfoBDpMi-WFXdyLn6gs0mQTXgLtCHovx714nTWoobDgiW2dlhNEfReo1CioDvCV2LuVF7UrZ2wEul6F2yDdLl_pwa7DAul6vVU4nx09Mb5NUacjIfSBJK5toQ9oqwwuATtRgeHi-9pY8InmEw1_naRGUcxAPCtTPrlLF8Y10hgnIaMu92Zj_S3ZAMqpajwvtSrt_gXzDlMBhJS6iS23i95UmN_7pi_wADf1YWEniDdZ6P72VxfpjwMEmxCXPts55VBRy8f5sff981xoMb605ZDL1qGd4jqWi8C_esmiqGG7FTK2eF_eNhRqgi_lbCjI1T6lu4WAiLZJXRHMrj0FwLToXFWkg-atyp1MVa1O7E0CGg22_XChkp4UKkXjPfmGEhd6oLXEVtoqeXS9DPeT_9ABUC_8M)

## References

- [KEP 3063 - Dynamic Resource Allocation #306](https://github.com/kubernetes/enhancements/blob/master/keps/sig-node/3063-dynamic-resource-allocation/README.md)
- [KEP 3695 - DRA: structured parameters #438](https://github.com/kubernetes/enhancements/issues/4381)
- [Extend PodResources to include resources from Dynamic Resource Allocation (DRA)](https://github.com/kubernetes/enhancements/issues/3695)
- [Working Group Device Management](https://github.com/kubernetes-sigs/wg-device-management)
- [Kubernetes Network Drivers, Antonio Ojea, Presentation](https://docs.google.com/presentation/d/1Vdr7BhbYXeWjwmLjGmqnUkvJr_eOUdU0x-JxfXWxUT8/edit?usp=sharing)
- [The Future of Kubernetes Networking - Antonio Ojea, Googe & Dan Winship, Red Hat - Kubernetes Contributor Summit EU 2024](https://sched.co/1aOqO)
- [Better Together! GPU, TPU and NIC Topological Alignment with DRA - John Belamaric, Google & Patrick Ohly, Intel - Kubecon US 2024](https://sched.co/1i7pv)

## Disclaimer

This is not an officially supported Google product. This project is not
eligible for the [Google Open Source Software Vulnerability Rewards
Program](https://bughunters.google.com/open-source-security).