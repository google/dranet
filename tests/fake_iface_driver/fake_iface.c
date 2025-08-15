/*
 * A simple network device driver that allows creating `fake_iface` network
 * devices. These devices have a permanent hardware address. This is useful for
 * testing DraNet without requiring specific physical hardware. For most other
 * purposes, the device behaves like a dummy interface. 
 */
 
#include <linux/module.h>
#include <linux/kernel.h>
#include <linux/netdevice.h>
#include <linux/etherdevice.h>
#include <linux/ethtool.h>
#include <net/rtnetlink.h>

static int fake_iface_stop(struct net_device *dev)
{
	pr_info("fake_iface: device closed\n");
	netif_stop_queue(dev);
	return 0;
}

static int fake_iface_init_dev(struct net_device *dev)
{
	pr_info("fake_iface: device initialized\n");
	return 0;
}

static netdev_tx_t fake_iface_xmit(struct sk_buff *skb, struct net_device *dev)
{
	pr_info("fake_iface: dummy xmit called\n");
	dev_kfree_skb(skb);
	return NETDEV_TX_OK;
}

static void fake_iface_set_multicast_list(struct net_device *dev)
{
	pr_info("fake_iface: set multicast list called\n");
}

static void fake_iface_get_stats64(struct net_device *dev,
				   struct rtnl_link_stats64 *stats)
{
	pr_info("fake_iface: get stats64 called\n");
}

static int fake_iface_change_carrier(struct net_device *dev, bool new_carrier)
{
	pr_info("fake_iface: change carrier called\n");
	return 0;
}

static const struct net_device_ops fake_iface_netdev_ops = {
	.ndo_stop = fake_iface_stop,
	.ndo_init = fake_iface_init_dev,
	.ndo_start_xmit = fake_iface_xmit,
	.ndo_validate_addr = eth_validate_addr,
	.ndo_set_rx_mode = fake_iface_set_multicast_list,
	.ndo_set_mac_address = eth_mac_addr,
	.ndo_get_stats64 = fake_iface_get_stats64,
	.ndo_change_carrier = fake_iface_change_carrier,
};

static void fake_iface_setup(struct net_device *dev)
{
	pr_info("fake_iface: setup called\n");
	// Apply standard ethernet device configurations.
	ether_setup(dev);

	// Set a random permanent MAC address.
	eth_hw_addr_random(dev);
	memcpy((void *)dev->perm_addr, dev->dev_addr, ETH_ALEN);
	dev->addr_assign_type = NET_ADDR_PERM;

	// Configure no upper limit for MTU by setting to 0.
	dev->min_mtu = 0;
	dev->max_mtu = 0;
	
	// Set features which this device supports and which ethtool can modify.
	dev->features|= NETIF_F_SG | NETIF_F_FRAGLIST;
	dev->features|= NETIF_F_GSO_SOFTWARE;
	dev->features|= NETIF_F_HW_CSUM | NETIF_F_HIGHDMA;
	dev->features|= NETIF_F_GSO_ENCAP_ALL;
	dev->hw_features |= dev->features;
	dev->hw_enc_features |= dev->features;

	dev->netdev_ops = &fake_iface_netdev_ops;
}

static int fake_iface_newlink(struct net *src_net, struct net_device *dev,
			    struct nlattr *tb[],
			    struct nlattr *data[],
			    struct netlink_ext_ack *extack)
{
	pr_info("fake_iface: newlink called\n");
	int err = register_netdevice(dev);
	if (err) {
		pr_err("fake_iface: failed to register netdevice: %d\n", err);
	}
	return 0;
}

static void fake_iface_dellink(struct net_device *dev, struct list_head *head)
{
	pr_info("fake_iface: dellink called\n");
	unregister_netdevice_queue(dev, head);
}

static int fake_iface_rtnl_validate(struct nlattr *tb[],
				    struct nlattr *data[],
				    struct netlink_ext_ack *extack)
{
	pr_info("fake_iface: validate called\n");
	return 0;
}

static size_t fake_iface_rtnl_get_size(const struct net_device *dev)
{
	pr_info("fake_iface: get_size called\n");
	return 0;
}

static int fake_iface_rtnl_fill_info(struct sk_buff *skb,
				     const struct net_device *dev)
{
	pr_info("fake_iface: fill_info called\n");
	return 0;
}

static struct rtnl_link_ops fake_iface_link_ops = {
    .kind		= "fake_iface",
    .setup		= fake_iface_setup,
    .validate	= fake_iface_rtnl_validate,
    .newlink	= fake_iface_newlink,
    .dellink	= fake_iface_dellink,
    .get_size	= fake_iface_rtnl_get_size,
    .fill_info	= fake_iface_rtnl_fill_info,
};

static int __init fake_iface_init(void)
{
	int err;

	pr_info("fake_iface: Registering fake interface driver\n");

	err = rtnl_link_register(&fake_iface_link_ops);
	if (err) {
		pr_err("fake_iface: Failed to register link ops: %d\n", err);
		return err;
	}

	return 0;
}

static void __exit fake_iface_exit(void)
{
    rtnl_link_unregister(&fake_iface_link_ops);
    pr_info("fake_iface: Unregistered fake interface driver.\n");
}

// Register the init and exit functions
module_init(fake_iface_init);
module_exit(fake_iface_exit);

// Module metadata
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("A fake interface driver for testing.");
