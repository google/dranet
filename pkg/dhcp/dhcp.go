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

package dhcp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/vishvananda/netns"
	"k8s.io/klog/v2"
)

const (
	// DHCP op codes
	opBootRequest = 1
	opBootReply   = 2

	// Hardware address types
	htypeEthernet = 1
	hlenEthernet  = 6

	// DHCP message types (Option 53)
	dhcpDiscover = 1
	dhcpOffer    = 2
	dhcpRequest  = 3
	dhcpACK      = 5

	// DHCP options
	optMessageType          = 53
	optRequestedIPAddress   = 50
	optServerIdentifier     = 54
	optSubnetMask           = 1
	optRouter               = 3
	optParameterRequestList = 55
	optEnd                  = 255
	optLeaseTime            = 51

	// DHCP ports
	dhcpClientPort = 68
	dhcpServerPort = 67

	// Magic cookie for DHCP options
	magicCookie = 0x63825363 // 99.130.83.99
)

// DHCPOption represents a DHCP option (Type, Length, Value)
type DHCPOption struct {
	Type   byte
	Length byte
	Value  []byte
}

/*

  0                   1                   2                   3
   0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
   |     op (1)    |   htype (1)   |   hlen (1)    |   hops (1)    |
   +---------------+---------------+---------------+---------------+
   |                            xid (4)                            |
   +-------------------------------+-------------------------------+
   |           secs (2)            |           flags (2)           |
   +-------------------------------+-------------------------------+
   |                          ciaddr  (4)                          |
   +---------------------------------------------------------------+
   |                          yiaddr  (4)                          |
   +---------------------------------------------------------------+
   |                          siaddr  (4)                          |
   +---------------------------------------------------------------+
   |                          giaddr  (4)                          |
   +---------------------------------------------------------------+
   |                                                               |
   |                          chaddr  (16)                         |
   |                                                               |
   |                                                               |
   +---------------------------------------------------------------+
   |                                                               |
   |                          sname   (64)                         |
   +---------------------------------------------------------------+
   |                                                               |
   |                          file    (128)                        |
   +---------------------------------------------------------------+
   |                                                               |
   |                          options (variable)                   |
   +---------------------------------------------------------------+

                  Figure 1:  Format of a DHCP message
									https://datatracker.ietf.org/doc/html/rfc2131
*/
// DHCPPacket represents the structure of a DHCP message
type DHCPPacket struct {
	Op      byte
	Htype   byte
	Hlen    byte
	Hops    byte
	Xid     uint32
	Secs    uint16
	Flags   uint16
	Ciaddr  net.IP           // Client IP address
	Yiaddr  net.IP           // Your (client) IP address
	Siaddr  net.IP           // Server IP address
	Giaddr  net.IP           // Gateway IP address
	Chaddr  net.HardwareAddr // Client hardware address
	Sname   [64]byte         // Server host name
	File    [128]byte        // Boot file name
	Options []DHCPOption
}

// Marshal serializes a DHCPPacket into a byte slice
func (p *DHCPPacket) Marshal() ([]byte, error) {
	buf := new(bytes.Buffer)

	// Write fixed-size header fields
	binary.Write(buf, binary.BigEndian, p.Op)
	binary.Write(buf, binary.BigEndian, p.Htype)
	binary.Write(buf, binary.BigEndian, p.Hlen)
	binary.Write(buf, binary.BigEndian, p.Hops)
	binary.Write(buf, binary.BigEndian, p.Xid)
	binary.Write(buf, binary.BigEndian, p.Secs)
	binary.Write(buf, binary.BigEndian, p.Flags)
	binary.Write(buf, binary.BigEndian, p.Ciaddr.To4())
	binary.Write(buf, binary.BigEndian, p.Yiaddr.To4())
	binary.Write(buf, binary.BigEndian, p.Siaddr.To4())
	binary.Write(buf, binary.BigEndian, p.Giaddr.To4())

	// Write chaddr (16 bytes, pad with zeros if less than 16)
	chaddrBuf := make([]byte, 16)
	copy(chaddrBuf, p.Chaddr)
	binary.Write(buf, binary.BigEndian, chaddrBuf)

	// Write sname and file (padded with zeros)
	binary.Write(buf, binary.BigEndian, p.Sname)
	binary.Write(buf, binary.BigEndian, p.File)

	// Write magic cookie
	binary.Write(buf, binary.BigEndian, magicCookie)

	// Write options
	for _, opt := range p.Options {
		binary.Write(buf, binary.BigEndian, opt.Type)
		binary.Write(buf, binary.BigEndian, opt.Length)
		binary.Write(buf, binary.BigEndian, opt.Value)
	}
	binary.Write(buf, binary.BigEndian, optEnd) // End option

	return buf.Bytes(), nil
}

// Unmarshal parses a byte slice into a DHCPPacket
func (p *DHCPPacket) Unmarshal(data []byte) error {
	if len(data) < 240 { // Minimum DHCP packet size without options
		return fmt.Errorf("DHCP packet too short: %d bytes", len(data))
	}

	reader := bytes.NewReader(data)

	binary.Read(reader, binary.BigEndian, &p.Op)
	binary.Read(reader, binary.BigEndian, &p.Htype)
	binary.Read(reader, binary.BigEndian, &p.Hlen)
	binary.Read(reader, binary.BigEndian, &p.Hops)
	binary.Read(reader, binary.BigEndian, &p.Xid)
	binary.Read(reader, binary.BigEndian, &p.Secs)
	binary.Read(reader, binary.BigEndian, &p.Flags)

	ipBuf := make([]byte, 4)
	binary.Read(reader, binary.BigEndian, ipBuf)
	p.Ciaddr = net.IP(ipBuf)
	binary.Read(reader, binary.BigEndian, ipBuf)
	p.Yiaddr = net.IP(ipBuf)
	binary.Read(reader, binary.BigEndian, ipBuf)
	p.Siaddr = net.IP(ipBuf)
	binary.Read(reader, binary.BigEndian, ipBuf)
	p.Giaddr = net.IP(ipBuf)

	chaddrBuf := make([]byte, 16)
	binary.Read(reader, binary.BigEndian, chaddrBuf)
	p.Chaddr = net.HardwareAddr(chaddrBuf[:p.Hlen]) // Use Hlen for actual MAC length

	binary.Read(reader, binary.BigEndian, p.Sname[:])
	binary.Read(reader, binary.BigEndian, p.File[:])

	var cookie uint32
	binary.Read(reader, binary.BigEndian, &cookie)
	if cookie != magicCookie {
		return fmt.Errorf("invalid DHCP magic cookie: 0x%x", cookie)
	}

	// Parse options
	for reader.Len() > 0 {
		var optType byte
		if err := binary.Read(reader, binary.BigEndian, &optType); err != nil {
			return fmt.Errorf("failed to read option type: %v", err)
		}
		if optType == optEnd {
			break
		}

		var optLen byte
		if err := binary.Read(reader, binary.BigEndian, &optLen); err != nil {
			return fmt.Errorf("failed to read option length for type %d: %v", optType, err)
		}

		optValue := make([]byte, optLen)
		if _, err := reader.Read(optValue); err != nil {
			return fmt.Errorf("failed to read option value for type %d, length %d: %v", optType, optLen, err)
		}
		p.Options = append(p.Options, DHCPOption{Type: optType, Length: optLen, Value: optValue})
	}

	return nil
}

// GetOptionValue retrieves the value of a specific DHCP option
func (p *DHCPPacket) GetOptionValue(optionType byte) []byte {
	for _, opt := range p.Options {
		if opt.Type == optionType {
			return opt.Value
		}
	}
	return nil
}

// newXid generates a random transaction ID
func newXid() uint32 {
	return rand.Uint32()
}

// createDiscoverPacket creates a DHCP DISCOVER packet
func createDiscoverPacket(mac net.HardwareAddr, xid uint32) *DHCPPacket {
	p := &DHCPPacket{
		Op:     opBootRequest,
		Htype:  htypeEthernet,
		Hlen:   hlenEthernet,
		Hops:   0,
		Xid:    xid,
		Secs:   0,
		Flags:  0x8000, // Broadcast flag
		Ciaddr: net.IPv4zero,
		Yiaddr: net.IPv4zero,
		Siaddr: net.IPv4zero,
		Giaddr: net.IPv4zero,
		Chaddr: mac,
	}

	// Message Type: Discover
	p.Options = append(p.Options, DHCPOption{Type: optMessageType, Length: 1, Value: []byte{dhcpDiscover}})
	// Parameter Request List: Subnet Mask, Router, DNS Server, Lease Time
	p.Options = append(p.Options, DHCPOption{Type: optParameterRequestList, Length: 4, Value: []byte{optSubnetMask, optRouter, optLeaseTime, 6}}) // 6 is DNS Servers

	return p
}

// createRequestPacket creates a DHCP REQUEST packet
func createRequestPacket(offer *DHCPPacket, mac net.HardwareAddr, xid uint32) *DHCPPacket {
	p := &DHCPPacket{
		Op:     opBootRequest,
		Htype:  htypeEthernet,
		Hlen:   hlenEthernet,
		Hops:   0,
		Xid:    xid,
		Secs:   0,
		Flags:  0x8000, // Broadcast flag
		Ciaddr: net.IPv4zero,
		Yiaddr: net.IPv4zero, // Should be 0.0.0.0 for REQUEST
		Siaddr: net.IPv4zero,
		Giaddr: net.IPv4zero,
		Chaddr: mac,
	}

	// Message Type: Request
	p.Options = append(p.Options, DHCPOption{Type: optMessageType, Length: 1, Value: []byte{dhcpRequest}})
	// Requested IP Address
	if offeredIP := offer.Yiaddr; offeredIP != nil {
		p.Options = append(p.Options, DHCPOption{Type: optRequestedIPAddress, Length: 4, Value: offeredIP.To4()})
	}
	// Server Identifier
	if serverID := offer.GetOptionValue(optServerIdentifier); serverID != nil {
		p.Options = append(p.Options, DHCPOption{Type: optServerIdentifier, Length: 4, Value: serverID})
	}
	// Parameter Request List (same as discover)
	p.Options = append(p.Options, DHCPOption{Type: optParameterRequestList, Length: 4, Value: []byte{optSubnetMask, optRouter, optLeaseTime, 6}})

	return p
}

/*
   3.1 Client-server interaction - allocating a network address


 1. The client broadcasts a DHCPDISCOVER message on its local physical
      subnet.  The DHCPDISCOVER message MAY include options that suggest
      values for the network address and lease duration.  BOOTP relay
      agents may pass the message on to DHCP servers not on the same
      physical subnet.

   2. Each server may respond with a DHCPOFFER message that includes an
      available network address in the 'yiaddr' field (and other
      configuration parameters in DHCP options).  Servers need not
      reserve the offered network address, although the protocol will
      work more efficiently if the server avoids allocating the offered
      network address to another client.  When allocating a new address,
      servers SHOULD check that the offered network address is not
      already in use; e.g., the server may probe the offered address
      with an ICMP Echo Request.  Servers SHOULD be implemented so that
      network administrators MAY choose to disable probes of newly
      allocated addresses.  The server transmits the DHCPOFFER message
      to the client, using the BOOTP relay agent if necessary.

   Message         Use
   -------         ---

   DHCPDISCOVER -  Client broadcast to locate available servers.

   DHCPOFFER    -  Server to client in response to DHCPDISCOVER with
                   offer of configuration parameters.

   DHCPREQUEST  -  Client message to servers either (a) requesting
                   offered parameters from one server and implicitly
                   declining offers from all others, (b) confirming
                   correctness of previously allocated address after,
                   e.g., system reboot, or (c) extending the lease on a
                   particular network address.

   DHCPACK      -  Server to client with configuration parameters,
                   including committed network address.

   DHCPNAK      -  Server to client indicating client's notion of network
                   address is incorrect (e.g., client has moved to new
                   subnet) or client's lease as expired

   DHCPDECLINE  -  Client to server indicating network address is already
                   in use.

   DHCPRELEASE  -  Client to server relinquishing network address and
                   cancelling remaining lease.

   DHCPINFORM   -  Client to server, asking only for local configuration
                   parameters; client already has externally configured
                   network address.

                          Table 2:  DHCP messages

*/

func AcquireNewIP(containerNsPAth string, ifName string, macAddr net.HardwareAddr) (acquiredIP *net.IPNet, err error) {
	defer func() {
		if r := recover(); r != nil {
			klog.Infof("Recovered from panic: %v", r)
			acquiredIP = nil
			err = fmt.Errorf("panic occurred: %v", r)
		}
		if err != nil {
			klog.Infof("fail to acquire ip on ns %s for iface %s : %v", containerNsPAth, ifName, err)
		}
	}()

	// Create UDP socket in the target network namespace
	sockFD, err := socketAt(syscall.AF_INET, syscall.SOCK_DGRAM, 0, containerNsPAth)
	if err != nil {
		return nil, fmt.Errorf("fail to create socket in namespace '%s': %v", containerNsPAth, err)
	}
	defer syscall.Close(sockFD) // Close the socket FD

	// Go's network poller expects non-blocking file descriptors.
	if err := syscall.SetNonblock(sockFD, true); err != nil {
		return nil, fmt.Errorf("fail setting non-blocking: %v", err)
	}
	// Bind to the specific device
	if err := syscall.SetsockoptString(sockFD, syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, ifName); err != nil {
		return nil, fmt.Errorf("gailed to set SO_BINDTODEVICE to '%s': %v", ifName, err)
	}
	// Set socket options: SO_REUSEADDR and SO_BROADCAST
	if err := syscall.SetsockoptInt(sockFD, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		return nil, fmt.Errorf("failed to set SO_REUSEADDR: %v", err)
	}
	if err := syscall.SetsockoptInt(sockFD, syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1); err != nil {
		return nil, fmt.Errorf("failed to set SO_BROADCAST: %v", err)
	}

	// Bind the socket to the client port (68) on 0.0.0.0 in the target namespace
	var sockaddr syscall.SockaddrInet4
	sockaddr.Port = dhcpClientPort
	copy(sockaddr.Addr[:], net.IPv4zero.To4())

	if err := syscall.Bind(sockFD, &sockaddr); err != nil {
		return nil, fmt.Errorf("failed to bind socket to 0.0.0.0:%d in namespace: %v", dhcpClientPort, err)
	}
	klog.V(4).Infof("Socket bound to 0.0.0.0:%d in namespace.\n", dhcpClientPort)

	file := os.NewFile(uintptr(sockFD), "dhcp-socket")
	if file == nil {
		return nil, fmt.Errorf("error creating os.File from file descriptor")
	}

	// use golang library to avoid working with low level syscalls
	udpConn, err := net.FilePacketConn(file)
	if err != nil {
		return nil, fmt.Errorf("fail to create PacketConn on socket: %v", err)
	}
	file.Close()
	defer udpConn.Close()

	clientXid := newXid()
	// --- DHCP DISCOVER ---
	discoverPacket := createDiscoverPacket(macAddr, clientXid)
	discoverBytes, err := discoverPacket.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal DISCOVER packet: %v", err)
	}

	klog.V(4).Infoln("Sending DHCP DISCOVER...")
	broadcastDestAddr := &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpServerPort}
	_, err = udpConn.WriteTo(discoverBytes, broadcastDestAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to send DISCOVER packet: %v", err)
	}

	// --- Wait for DHCP OFFER ---
	offer := &DHCPPacket{}
	buffer := make([]byte, 1500) // Max DHCP packet size

	// Default NRI request timeout is 2 seconds, so we can not block
	// for a long time or the server will disconnect us. The application
	// should handle this but we can do just a best effort, this is specially
	// problematic for GCE VMs.
	readDeadline := time.Now().Add(1 * time.Second)
	if err := udpConn.SetReadDeadline(readDeadline); err != nil {
		return nil, fmt.Errorf("failed to set read deadline for OFFER: %v", err)
	}

	klog.V(4).Infoln("Waiting for DHCP OFFER...")
	n, fromAddr, err := udpConn.ReadFrom(buffer)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			klog.Infof("failed to receive OFFER packet: Timeout after 5 seconds")
			return nil, nil
		}
		return nil, fmt.Errorf("failed to receive OFFER packet: %v", err)
	}
	klog.V(4).Infoln("Received packet ...")
	if err := offer.Unmarshal(buffer[:n]); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OFFER packet: %v", err)
	}

	msgType := offer.GetOptionValue(optMessageType)
	if len(msgType) == 0 || msgType[0] != dhcpOffer {
		return nil, fmt.Errorf("received packet is not a DHCP OFFER (type: %v)", msgType)
	}

	klog.V(4).Infoln("Received DHCP OFFER...")
	if offer.Xid != clientXid {
		return nil, fmt.Errorf("received OFFER with mismatched XID: expected 0x%x, got 0x%x", clientXid, offer.Xid)
	}

	// Extract server IP from 'from' address
	var offerServerIP net.IP
	if udpFromAddr, ok := fromAddr.(*net.UDPAddr); ok {
		offerServerIP = udpFromAddr.IP
	} else {
		offerServerIP = offer.Siaddr // Fallback to Siaddr if from address is not IPv4
	}
	klog.V(4).Infof("received DHCP OFFER from %s. Offered IP: %s\n", offerServerIP, offer.Yiaddr)

	// --- DHCP REQUEST ---
	requestPacket := createRequestPacket(offer, macAddr, clientXid)
	requestBytes, err := requestPacket.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal REQUEST packet: %v", err)
	}

	klog.V(4).Infoln("Sending DHCP REQUEST...")
	// Send REQUEST to the specific server that offered, not broadcast
	requestDestAddr := &net.UDPAddr{IP: offerServerIP, Port: dhcpServerPort}
	_, err = udpConn.WriteTo(requestBytes, requestDestAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to send REQUEST packet: %v", err)
	}

	// --- Wait for DHCP ACK ---
	ack := &DHCPPacket{}
	// Set read deadline for the socket again for ACK
	// We can not take longer than the NRI request timeout that is 2 second by default
	readDeadline = time.Now().Add(500 * time.Millisecond)
	if err := udpConn.SetReadDeadline(readDeadline); err != nil {
		return nil, fmt.Errorf("failed to set read deadline for ACK: %v", err)
	}

	klog.V(4).Infoln("Waiting for DHCP ACK in target namespace...")
	n, _, err = udpConn.ReadFrom(buffer) // fromAddr might not be strictly needed for ACK validation against serverIP
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, fmt.Errorf("failed to receive ACK packet: Timeout after 5 seconds")
		}
		return nil, fmt.Errorf("failed to receive ACK packet: %v", err)
	}
	klog.V(4).Infoln("Received packet ...")
	if err := ack.Unmarshal(buffer[:n]); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ACK packet: %v", err)
	}

	msgType = ack.GetOptionValue(optMessageType)
	if len(msgType) == 0 || msgType[0] != dhcpACK {
		return nil, fmt.Errorf("received packet is not a DHCP ACK (type: %v)", msgType)
	}
	klog.V(4).Infoln("Received DHCP ACK...")
	if ack.Xid != clientXid {
		return nil, fmt.Errorf("received ACK with mismatched XID: expected 0x%x, got 0x%x", clientXid, ack.Xid)
	}

	assignedIP := ack.Yiaddr
	subnetMaskBytes := ack.GetOptionValue(optSubnetMask)
	routerBytes := ack.GetOptionValue(optRouter)

	if assignedIP == nil || assignedIP.IsUnspecified() || len(subnetMaskBytes) != 4 {
		return nil, fmt.Errorf("message DHCP ACK did not provide valid IP address or subnet mask")
	}

	subnetMask := net.IPv4(subnetMaskBytes[0], subnetMaskBytes[1], subnetMaskBytes[2], subnetMaskBytes[3])
	// TODO
	var routerIP net.IP
	if len(routerBytes) >= 4 { // Router option can have multiple IPs, take the first
		routerIP = net.IPv4(routerBytes[0], routerBytes[1], routerBytes[2], routerBytes[3])
	}

	klog.V(2).Infof("DHCP Assigned IP: %s\n", assignedIP)
	klog.V(2).Infof("DHCP Netmask: %s\n", subnetMask)
	klog.V(4).Infof("Router (Gateway): %s\n", routerIP)
	if leaseTimeBytes := ack.GetOptionValue(optLeaseTime); len(leaseTimeBytes) == 4 {
		leaseTime := time.Duration(binary.BigEndian.Uint32(leaseTimeBytes)) * time.Second
		klog.V(4).Infof("Lease Time: %s\n", leaseTime)
	}

	return &net.IPNet{IP: assignedIP, Mask: net.IPMask(subnetMask)}, nil
}

/*
3.2 Client-server interaction - reusing a previously allocated network
    address

   If a client remembers and wishes to reuse a previously allocated
   network address, a client may choose to omit some of the steps
   described in the previous section.  The timeline diagram in figure 4
   shows the timing relationships in a typical client-server interaction
   for a client reusing a previously allocated network address.

   1. The client broadcasts a DHCPREQUEST message on its local subnet.
      The message includes the client's network address in the
      'requested IP address' option. As the client has not received its
      network address, it MUST NOT fill in the 'ciaddr' field. BOOTP
      relay agents pass the message on to DHCP servers not on the same
      subnet.  If the client used a 'client identifier' to obtain its
      address, the client MUST use the same 'client identifier' in the
      DHCPREQUEST message.

   2. Servers with knowledge of the client's configuration parameters
      respond with a DHCPACK message to the client.  Servers SHOULD NOT
      check that the client's network address is already in use; the
      client may respond to ICMP Echo Request messages at this point.
*/

// RenewIP attempts to renew/reacquire a previously allocated IP address.
// This implements RFC 2131 Section 3.2 logic (INIT-REBOOT state).
// It broadcasts a DHCPREQUEST with 'requested IP address' option.
func RenewIP(containerNsPAth string, ifName string, ip net.IP) error {
	// TODO
	return fmt.Errorf("not implemented")
}

// SocketAt creates a socket in the namespace passed as argument.
// ref: https://lore.kernel.org/patchwork/patch/217025/
func socketAt(domain, typ, proto int, containerNsPAth string) (int, error) {
	// lock the thread so we don't switch namespaces
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// get current namespace
	origin, err := netns.Get()
	if err != nil {
		return -1, err
	}

	containerNs, err := netns.GetFromPath(containerNsPAth)
	if err != nil {
		return -1, fmt.Errorf("could not get network namespace from path %s : %w", containerNsPAth, err)
	}
	defer containerNs.Close()

	defer func() {
		netns.Set(origin)
		origin.Close()
	}()

	netns.Set(containerNs)
	return syscall.Socket(domain, typ, proto)
}
