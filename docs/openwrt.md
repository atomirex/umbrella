#### OpenWrt
While it can run on OpenWrt it will not run on every OpenWrt device, and it won't run well on many it could run on. The target device has been a D-Link AX3200 M32, which has a dual core AArch64 (Arm) CPU and 512MB of RAM. Thanks to the golang cross compilation support this is a basic arm64 linux target and can be built with build_arm64_linux.sh .

Once setup the steps are the same as for running locally on Linux. (See below).

OpenWrt is a generally well behaved small Linux distro, with the exceptions here being aspects related to the security requirements for browser WebRTC support. OpenWrt uses different things for core services than you may expect, such as the package manager being opkg. Assuming you have OpenWrt installed, working, and ssh access to the device . . . 

##### umdns and all lower case hostname
Out of the box OpenWrt sets the hostname "OpenWrt" and does not include mdns support. Luckily there is a convenient package umdns: https://openwrt.org/docs/guide-developer/mdns

Unfortunately, at least with Apple devices as clients, umdns causes confusion with letter case in hostnames, which will then cause the certificate checks to fail, so the security prerequisites are no longer valid. The workaround is to set the OpenWrt device hostname to all lower case letters. (All the certs will need to reflect this).

It's useful to access the services of one AP from machines connected to other APs. To advertise mdns back out to the WAN to do this /etc/config/umdns has to include something like:
```
config umdns
	option jail 1
	list network lan
	list network wan
```

##### Firewall
This also required allowing mdns, so /etc/config/firewall has this in it for multicast on the usual IP/port combo:
```
config rule
	option src_port '5353'
	option src '*'
	option name 'Allow-mDNS'
        option target 'ACCEPT'
        option dest_ip '224.0.0.251'
        option dest_port '5353'
        option proto 'udp'
```
