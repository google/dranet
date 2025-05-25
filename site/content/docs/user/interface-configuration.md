---
title: "Interface Configuration"
date: 2025-05-25T11:30:40Z
---

To configure network interfaces in DraNet, users can provide custom configurations through the parameters field of a ResourceClaim or ResourceClaimTemplate. This configuration adheres to the NetworkConfig structure, which defines the desired state for network interfaces and their associated routes.

### Network Configuration Overview

The primary structure for custom network configuration is NetworkConfig. It encompasses settings for the network interface itself and any specific routes to be applied within the Pod's network namespace.

```go
type NetworkConfig struct {  
	Interface InterfaceConfig `json:"interface"`
	Routes    []RouteConfig   `json:"routes"`
}
```

#### Interface Configuration

The InterfaceConfig structure allows you to specify details for a single network interface.

```go
type InterfaceConfig struct {  
	Name         string   `json:"name,omitempty"`
	Addresses    []string `json:"addresses,omitempty"`
	MTU          int32    `json:"mtu,omitempty"`
	HardwareAddr string   `json:"hardwareAddr,omitempty"`
}
```

* **name** (string, optional): The logical name that the interface will have inside the Pod (e.g., "eth0", "enp0s3"). If not specified, DraNet will keep the original name if compliant.  
* **addresses** ([]string, optional): A list of IP addresses in CIDR format (e.g., "192.168.1.10/24", "2001:db8::1/64") to be assigned to the interface.  
* **mtu** (int32, optional): The Maximum Transmission Unit for the interface.  
* **hardwareAddr** (string, optional, Read-Only): The current hardware (MAC) address of the interface.

#### **Route Configuration (RouteConfig)**

The RouteConfig structure defines individual network routes to be added to the Pod's network namespace, associated with the configured interface.

```go
type RouteConfig struct {  
	Destination string `json:"destination,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	Source      string `json:"source,omitempty"`
	Scope       uint8  `json:"scope,omitempty"`
}
```

* **destination** (string, optional): The destination network in CIDR format (e.g., "0.0.0.0/0" for a default route, "10.0.0.0/8" for a specific subnet).  
* **gateway** (string, optional): The IP address of the gateway for the route. This field is mandatory for routes with Universe scope (0).  
* **source** (string, optional): An optional source IP address for policy routing.  
* **scope** (uint8, optional): The scope of the route. Only Link (253) or Universe (0) are allowed.  
  * Link (253): Routes directly to a device without a gateway (e.g., for directly connected subnets).  
  * Universe (0): Routes to a network via a gateway.

### **Example: Customizing a Network Interface and Routes**

Below is an example of a ResourceClaim that allocates a dummy interface, renames it to "eth99", assigns a static IP address, and configures two routes: one to a subnet via a gateway and another link-scoped route.


```yaml
apiVersion: resource.k8s.io/v1beta1  
kind: ResourceClaim  
metadata:  
  name: dummy-interface-static-ip-route  
spec:  
  devices:  
    requests:  
      - name: req-dummy  
        deviceClassName: dra.net  
        selectors:  
          - cel:  
              expression: device.attributes["dra.net"].type == "dummy"  
    config:  
      - opaque:  
          driver: dra.net  
          parameters:  
            interface:  
              name: "eth99"  
              addresses:  
                - "169.254.169.13/32"  
            routes:  
              - destination: "169.254.169.0/24"  
                gateway: "169.254.169.1"  
              - destination: "169.254.169.1/32"  
                scope: 253  
---  
apiVersion: v1  
kind: Pod  
metadata:  
  name: pod3  
  labels:  
    app: pod  
spec:  
  containers:  
    - name: ctr1  
      image: registry.k8s.io/e2e-test-images/agnhost:2.39  
  resourceClaims:  
    - name: dummy1  
      resourceClaimName: dummy-interface-static-ip-route  
```
