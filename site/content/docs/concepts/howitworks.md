---
title: "How It Works"
date: 2024-12-19T11:20:46Z
---

The networking DRA driver uses GRPC to communicate with the Kubelet via the [DRA API](https://github.com/kubernetes/kubernetes/blob/f141907ddd89998e821eb1047885722c8ba8922b/staging/src/k8s.io/kubelet/pkg/apis/dra/v1/api.proto) and the Container Runtime via [NRI](https://github.com/containerd/nri). This architecture facilitates the supportability and reduces the complexity of the solution, it also makes it fully compatible and agnostic of the existing CNI plugins in the cluster.

The DRA driver, once the Pod network namespaces has been created, will receive a GRPC call from the Container Runtime via NRI to execute the corresponding configuration. A more detailed diagram can be found in:

[![](https://mermaid.ink/img/pako:eNp9UstuwyAQ_JUVp1ZNfoBDpMi-WFXdyLn6gs0mQTXgLtCHovx714nTWoobDgiW2dlhNEfReo1CioDvCV2LuVF7UrZ2wEul6F2yDdLl_pwa7DAul6vVU4nx09Mb5NUacjIfSBJK5toQ9oqwwuATtRgeHi-9pY8InmEw1_naRGUcxAPCtTPrlLF8Y10hgnIaMu92Zj_S3ZAMqpajwvtSrt_gXzDlMBhJS6iS23i95UmN_7pi_wADf1YWEniDdZ6P72VxfpjwMEmxCXPts55VBRy8f5sff981xoMb605ZDL1qGd4jqWi8C_esmiqGG7FTK2eF_eNhRqgi_lbCjI1T6lu4WAiLZJXRHMrj0FwLToXFWkg-atyp1MVa1O7E0CGg22_XChkp4UKkXjPfmGEhd6oLXEVtoqeXS9DPeT_9ABUC_8M?type=png)](https://mermaid.live/edit#pako:eNp9UstuwyAQ_JUVp1ZNfoBDpMi-WFXdyLn6gs0mQTXgLtCHovx714nTWoobDgiW2dlhNEfReo1CioDvCV2LuVF7UrZ2wEul6F2yDdLl_pwa7DAul6vVU4nx09Mb5NUacjIfSBJK5toQ9oqwwuATtRgeHi-9pY8InmEw1_naRGUcxAPCtTPrlLF8Y10hgnIaMu92Zj_S3ZAMqpajwvtSrt_gXzDlMBhJS6iS23i95UmN_7pi_wADf1YWEniDdZ6P72VxfpjwMEmxCXPts55VBRy8f5sff981xoMb605ZDL1qGd4jqWi8C_esmiqGG7FTK2eF_eNhRqgi_lbCjI1T6lu4WAiLZJXRHMrj0FwLToXFWkg-atyp1MVa1O7E0CGg22_XChkp4UKkXjPfmGEhd6oLXEVtoqeXS9DPeT_9ABUC_8M)