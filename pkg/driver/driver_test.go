package driver

import (
	"context"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	registerapi "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

// fakeDraPlugin is a mock implementation of the pluginHelper interface for testing.
type fakePluginHelper struct {
	publishErr         error
	publishCalled      chan struct{}
	registrationStatus *registerapi.RegistrationStatus
}

func newFakePluginHelper() *fakePluginHelper {
	return &fakePluginHelper{
		publishCalled: make(chan struct{}, 1),
	}
}

func (m *fakePluginHelper) PublishResources(_ context.Context, _ resourceslice.DriverResources) error {
	if m.publishCalled != nil {
		m.publishCalled <- struct{}{}
	}
	return m.publishErr
}

func (m *fakePluginHelper) Stop() {}

func (m *fakePluginHelper) RegistrationStatus() *registerapi.RegistrationStatus {
	return m.registrationStatus
}

// mockNetDB is a mock implementation of the inventoryDB interface for testing.
type fakeInventoryDB struct {
	resources chan []resourcev1.Device
	podNetNs  map[string]string
}

func newFakeInventoryDB() *fakeInventoryDB {
	return &fakeInventoryDB{
		resources: make(chan []resourcev1.Device, 1),
		podNetNs:  make(map[string]string),
	}
}

func (m *fakeInventoryDB) Run(_ context.Context) error { return nil }

func (m *fakeInventoryDB) GetResources(_ context.Context) <-chan []resourcev1.Device {
	return m.resources
}

func (m *fakeInventoryDB) GetNetInterfaceName(_ string) (string, error) { return "", nil }

func (m *fakeInventoryDB) AddPodNetNs(podKey string, netNs string) {
	m.podNetNs[podKey] = netNs
}

func (m *fakeInventoryDB) RemovePodNetNs(podKey string) {
	delete(m.podNetNs, podKey)
}

func (m *fakeInventoryDB) GetPodNetNs(podKey string) string {
	return m.podNetNs[podKey]
}
