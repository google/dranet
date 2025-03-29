/*
Copyright 2025 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gke

import (
	"context"
	"encoding/base64"
	"fmt"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	_ "k8s.io/cloud-provider-gcp/pkg/clientauthplugin/gcp" // register GCP auth provider
)

var dranetYaml = `
---
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: dranet
rules:
  - apiGroups:
      - ""
    resources:
      - nodes
    verbs:
      - get
  - apiGroups:
      - "resource.k8s.io"
    resources:
      - resourceslices
    verbs:
      - list
      - watch
      - create
      - update
  - apiGroups:
      - "resource.k8s.io"
    resources:
      - resourceclaims
      - deviceclasses
    verbs:
      - get
  - apiGroups:
      - "resource.k8s.io"
    resources:
      - resourceclaims/status
    verbs:
      - patch
      - update
---
kind: ClusterRoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: dranet
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: dranet
subjects:
- kind: ServiceAccount
  name: dranet
  namespace: kube-system
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: dranet
  namespace: kube-system
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: dranet
  namespace: kube-system
  labels:
    tier: node
    app: dranet
    k8s-app: dranet
spec:
  selector:
    matchLabels:
      app: dranet
  template:
    metadata:
      labels:
        tier: node
        app: dranet
        k8s-app: dranet
    spec:
      nodeSelector:
        dra.net/acceleratorpod: "true"
      hostNetwork: true
      tolerations:
      - operator: Exists
        effect: NoSchedule
      serviceAccountName: dranet
      hostPID: true
      initContainers:
      - name: enable-nri
        image: busybox:stable
        volumeMounts:
        - mountPath: /etc
          name: etc
        securityContext:
          privileged: true
        command:
        - /bin/sh
        - -c
        - |
          set -o errexit
          set -o pipefail
          set -o nounset
          set -x
          if grep -q "io.containerd.nri.v1.nri" /etc/containerd/config.toml
          then
             echo "containerd config contains NRI reference already; taking no action"
          else
             echo "containerd config does not mention NRI, thus enabling it";
             printf '%s\n' "[plugins.\"io.containerd.nri.v1.nri\"]" "  disable = false" "  disable_connections = false" "  plugin_config_path = \"/etc/nri/conf.d\"" "  plugin_path = \"/opt/nri/plugins\"" "  plugin_registration_timeout = \"5s\"" "  plugin_request_timeout = \"5s\"" "  socket_path = \"/var/run/nri/nri.sock\"" >> /etc/containerd/config.toml
             echo "restarting containerd"
             nsenter -t 1 -m -u -i -n -p -- systemctl restart containerd
          fi
      containers:
      - name: dranet
        args:
        - /dranet
        - --v=4
        image: ghcr.io/google/dranet:stable
        resources:
          requests:
            cpu: "100m"
            memory: "50Mi"
        securityContext:
          capabilities:
            add: ["NET_ADMIN", "SYS_ADMIN"]
        readinessProbe:
          httpGet:
            path: /healthz
            port: 9177
        volumeMounts:
        - name: device-plugin
          mountPath: /var/lib/kubelet/plugins
        - name: plugin-registry
          mountPath: /var/lib/kubelet/plugins_registry
        - name: nri-plugin
          mountPath: /var/run/nri
        - name: netns
          mountPath: /var/run/netns
          mountPropagation: HostToContainer
      volumes:
      - name: device-plugin
        hostPath:
          path: /var/lib/kubelet/plugins
      - name: plugin-registry
        hostPath:
          path: /var/lib/kubelet/plugins_registry
      - name: nri-plugin
        hostPath:
          path: /var/run/nri
      - name: netns
        hostPath:
          path: /var/run/netns
      - name: etc
        hostPath:
          path: /etc
---
`

func getClusterClient(ctx context.Context, projectID, location, clusterID string) (kubernetes.Interface, error) {
	c, err := container.NewClusterManagerClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cluster Manager client: %v", err)
	}
	defer c.Close()

	req := &containerpb.GetClusterRequest{
		Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s", projectID, location, clusterID),
	}

	resp, err := c.GetCluster(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster %s: %v", clusterID, err)
	}

	if resp.MasterAuth == nil || resp.MasterAuth.ClusterCaCertificate == "" || resp.Endpoint == "" {
		return nil, fmt.Errorf("cluster information is incomplete")
	}

	config := api.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters:   map[string]*api.Cluster{},  // Clusters is a map of referencable names to cluster configs
		AuthInfos:  map[string]*api.AuthInfo{}, // AuthInfos is a map of referencable names to user configs
		Contexts:   map[string]*api.Context{},  // Contexts is a map of referencable names to context configs
	}

	name := fmt.Sprintf("gke_%s_%s_%s", projectID, location, clusterID)
	cert, err := base64.StdEncoding.DecodeString(resp.MasterAuth.ClusterCaCertificate)
	if err != nil {
		return nil, fmt.Errorf("failed to decode cluster CA certificate: %v", err)
	}

	config.Clusters[name] = &api.Cluster{
		CertificateAuthorityData: cert,
		Server:                   "https://" + resp.Endpoint,
	}
	// Just reuse the context name as an auth name.
	config.Contexts[name] = &api.Context{
		Cluster:  name,
		AuthInfo: name,
	}
	// GCP specific configation; use cloud platform scope.
	config.AuthInfos[name] = &api.AuthInfo{
		AuthProvider: &api.AuthProviderConfig{
			Name: "gcp",
			Config: map[string]string{
				"scopes": "https://www.googleapis.com/auth/cloud-platform",
			},
		},
	}

	cfg, err := clientcmd.NewNonInteractiveClientConfig(config, clusterName, &clientcmd.ConfigOverrides{CurrentContext: clusterName}, nil).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes configuration cluster=%s: %w", clusterName, err)
	}

	return kubernetes.NewForConfig(cfg)
}
