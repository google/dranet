---
title: "Interface Status"
date: 2025-05-25T11:30:40Z
---

### Understanding Interface Status Output

When DRANET allocates a network interface to a Pod via a `ResourceClaim`, it publishes the status of the allocated device within the `ResourceClaim`'s `status` field. This provides crucial insights into the readiness and configuration of the network interface from a Kubernetes perspective, adhering to the standardized device status defined in [KEP-4817](https://github.com/kubernetes/enhancements/blob/master/keps/sig-node/4817-resource-claim-device-status/README.md).

After a `ResourceClaim` is processed and a network device is allocated, its status is reflected under `ResourceClaim.status.devices`. This section contains `conditions` and `networkData` for each allocated device.

#### Conditions

The `conditions` array provides a timeline of the device's state and indicates whether specific aspects of its configuration and readiness have been met. DRANET reports the following conditions:

* **`Ready` (type `NetworkDeviceReady`)**: Indicates that the network device has been successfully moved into the Pod's network namespace and is configured.
* **`NetworkReady`**: Signifies that the network interface within the Pod has been successfully configured with its IP addresses and routes.
* **`RDMALinkReady`**: (Applies only when RDMA is used in exclusive mode) Indicates that the associated RDMA link device has been successfully moved into the Pod's network namespace.

Each condition includes:
* **`lastTransitionTime`**: The timestamp when the condition last changed.
* **`message`**: A human-readable message about the condition.
* **`reason`**: A single-word identifier for the reason for the condition's last transition.
* **`status`**: "True", "False", or "Unknown" indicating the state of the condition.
* **`type`**: The specific type of condition (e.g., "Ready", "NetworkReady").

#### Network Data (`networkData`)

The `networkData` field provides standardized output about the network interface as it appears inside the Pod. This data is crucial for applications that need to dynamically discover their assigned network interfaces.

* **`interfaceName`**: The actual name of the network interface inside the Pod (e.g., "eth0", "eth99").
* **`hardwareAddress`**: The MAC address of the network interface.
* **`ips`**: A list of IP addresses (in CIDR format) assigned to the interface.

### Example Output

Here's an example of the `status` section from a `ResourceClaim` named `dummy-interface-static-ip-route`, showing the conditions and network data for an allocated network interface:

```yaml
status:
  allocation:
    devices:
      config:
      - opaque:
          driver: dra.net
          parameters:
            interface:
              addresses:
              - 169.254.169.13/32
              name: eth99
            routes:
            - destination: 169.254.169.0/24
              gateway: 169.254.169.1
            - destination: 169.254.169.1/32
              scope: 253
        source: FromClaim
      results:
      - adminAccess: null
        device: dummy2
        driver: dra.net
        pool: dra-worker
        request: req-dummy
    nodeSelector:
      nodeSelectorTerms:
      - matchFields:
        - key: metadata.name
          operator: In
          values:
          - dra-worker
  devices:
  - conditions:
    - lastTransitionTime: "2025-05-24T10:25:51Z"
      message: ""
      reason: NetworkDeviceReady
      status: "True"
      type: Ready
    - lastTransitionTime: "2025-05-24T10:25:51Z"
      message: ""
      reason: NetworkReady
      status: "True"
      type: NetworkReady
    device: dummy2
    driver: dra.net
    networkData:
      hardwareAddress: 86:d7:86:e6:f3:dd
      interfaceName: eth99
      ips:
      - 169.254.169.13/32
    pool: dra-worker
  reservedFor:
  - name: pod3
    resource: pods
```
