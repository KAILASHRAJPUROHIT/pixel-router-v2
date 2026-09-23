#!/system/bin/sh
# Raise the NAT connection table ceiling so multiple devices browsing
# simultaneously don't hit connection limits on the hotspot.
sysctl -w net.netfilter.nf_conntrack_max=65536
