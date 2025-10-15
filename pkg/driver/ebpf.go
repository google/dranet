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

package driver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/google/dranet/internal/nlwrap"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"k8s.io/klog/v2"
)

// unpinBPFPrograms runs in the host namespace to delete all the pinned bpf programs
func unpinBPFPrograms(ifName string) error {
	device, err := nlwrap.LinkByName(ifName)
	if err != nil {
		return err
	}
	ifIndex := uint32(device.Attrs().Index)

	klog.V(2).Infof("Attempting to unpin eBPF programs from interface %s", ifName)
	return filepath.Walk("/sys/fs/bpf", func(pinPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		l, err := link.LoadPinnedLink(pinPath, &ebpf.LoadPinOptions{})
		if err != nil {
			klog.V(4).Infof("error getting link %s: %v", pinPath, err)
			return nil
		}

		linkInfo, err := l.Info()
		if err != nil {
			klog.Infof("error link info: %v", err)
			return nil
		}

		var linkIfIndex uint32
		switch linkInfo.Type {
		case link.TCXType:
			extra := linkInfo.TCX()
			if extra != nil {
				linkIfIndex = extra.Ifindex
			}
		case link.NetkitType:
			extra := linkInfo.Netkit()
			if extra != nil {
				linkIfIndex = extra.Ifindex
			}
		case link.XDPType:
			extra := linkInfo.XDP()
			if extra != nil {
				linkIfIndex = extra.Ifindex
			}
		default:
			return nil
		}
		if linkIfIndex != ifIndex {
			return nil
		}
		err = l.Unpin()
		if err != nil {
			klog.Infof("fail to unpin bpf link %v", err)
		} else {
			klog.V(2).Infof("succesfully unpin bpf from link %d", linkInfo.ID)
		}
		return nil
	})

}

// detachEBPFPrograms detaches all eBPF programs (TC and TCX) from a given network interface.
// It attempts to remove both classic TC filters and newer TCX programs.
// It runs inside the network namespace to avoid programs on the root namespace
// to cause issues detaching the programs.
func detachEBPFPrograms(containerNsPAth string, ifName string) error {
	origns, err := netns.Get()
	if err != nil {
		return fmt.Errorf("unexpected error trying to get namespace: %v", err)
	}
	defer origns.Close()
	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return fmt.Errorf("could not get network namespace from path %s for network device %s : %w", containerNsPAth, ifName, err)
	}
	defer containerNs.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = netns.Set(containerNs)
	if err != nil {
		return fmt.Errorf("failt to join network namespace %s : %v", containerNsPAth, err)
	}
	// Switch back to the original namespace
	defer netns.Set(origns) // nolint:errcheck

	var errs []error
	device, err := nlwrap.LinkByName(ifName)
	if err != nil {
		return err
	}

	// Detach TC filters (legacy)
	klog.V(2).Infof("Attempting to detach TC filters from interface %s", device.Attrs().Name)
	for _, parent := range []uint32{netlink.HANDLE_MIN_INGRESS, netlink.HANDLE_MIN_EGRESS} {
		filters, err := nlwrap.FilterList(device, parent)
		if err != nil {
			klog.V(4).Infof("Could not list TC filters for interface %s (parent %d): %v", device.Attrs().Name, parent, err)
			continue
		}
		for _, f := range filters {
			if bpfFilter, ok := f.(*netlink.BpfFilter); ok {
				klog.V(4).Infof("Deleting TC filter %s from interface %s (parent %d)", bpfFilter.Name, device.Attrs().Name, parent)
				if err := netlink.FilterDel(f); err != nil {
					klog.V(2).Infof("failed to delete TC filter %s on %s: %v", bpfFilter.Name, device.Attrs().Name, err)
				}
			}
		}
	}

	// Detach TCX programs
	klog.V(2).Infof("Attempting to detach TCX programs from interface %s", device.Attrs().Name)
	for _, attach := range []ebpf.AttachType{ebpf.AttachTCXIngress, ebpf.AttachTCXEgress} {
		klog.V(2).Infof("Attempting to detach programs from attachment %s interface %s", attach.String(), device.Attrs().Name)
		result, err := link.QueryPrograms(link.QueryOptions{
			Target: int(device.Attrs().Index),
			Attach: attach,
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, p := range result.Programs {
			klog.V(2).Infof("Attempting to detach program %d from interface %s", p.ID, device.Attrs().Name)
			err = tryDetach(p.ID, device.Attrs().Index, attach)
			if err != nil {
				klog.V(2).Infof("Failed to detach program %d from interface %s", p.ID, device.Attrs().Name)
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

func tryDetach(id ebpf.ProgramID, deviceIdx int, attach ebpf.AttachType) error {
	prog, err := ebpf.NewProgramFromID(id)
	if err != nil {
		klog.V(2).Infof("failed to get eBPF program with ID %d: %v", id, err)
		return err
	}

	if err := prog.Unpin(); err != nil {
		klog.Infof("failed to unpin eBPF program %s: %v", prog.String(), err)
		return err
	}

	err = link.RawDetachProgram(link.RawDetachProgramOptions{
		Target:  deviceIdx,
		Program: prog,
		Attach:  attach,
	})
	if err != nil {
		klog.V(2).Infof("failed to detach eBPF program with ID %d: %v", id, err)
	}

	err = prog.Close()
	if err != nil {
		klog.Infof("failed to close eBPF program %s: %v", prog.String(), err)
		return err
	}
	return nil
}
