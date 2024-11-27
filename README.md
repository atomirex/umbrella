# Umbrella

This is a hacked up proof of concept WebRTC SFU that can run on many things, such as a local OpenWrt AP or in the cloud with Docker Compose/Traefik, including having a group session that spans both at once by backhauling from the AP to the cloud.

It's not production ready, mainly having almost no security! It also lacks quite a lot of potential optimizations. If you need a production video conf system today try https://livekit.io or https://www.daily.co (unaffiliated with either). It's also not very selective, just forwarding everything to everyone.

This began as a fork of the pion example sfu-ws to see if it could run on OpenWrt. https://github.com/pion/example-webrtc-applications/tree/master/sfu-ws

## What is it?

A Selective-Forwarding-Unit is a server that relays incoming traffic from one client by copying it to any number of other clients, system resources permitting. In the context of WebRTC it is decrypting the packets that it receives, and then re-encrypting them before forwarding them on, but it is not actually decoding and reencoding the media streams. A TURN ( https://en.wikipedia.org/wiki/Traversal_Using_Relays_around_NAT ) relay does not decode or decrypt a stream, but does not fan out the incoming traffic, only acting point-to-point, and so serves different purposes.

Reasons you might want a SFU:
* Isolating clients so they only see the IP of the SFU and not of each other - in a normal WebRTC session clients can see the IPs of other clients, which may present a security problem.
* A central location for recording streams - since we are decrypting and not decoding the streams you can cheaply dump the decrypted but compressed media packets to disk to replay later. (Pion includes other examples showing this).
* Reducing the amount of bandwidth needed at each node in contexts such as large group calls - in a normal P2P WebRTC session each additional participant must connect directly to every other participant causing an explosion in the number of connections it has to maintain, each of which is carrying a copy of the media. A central SFU provides a single point for all clients to connect to and absorbs the load of distributing the data, hopefully leveraging better connectivity.

![Diagram of relative at node cost of P2P vs SFU configurations](docs/sfu-diagram-1.svg?raw=true "The relative cost of P2P vs SFU configurations for edge nodes")

There are also reasons you might not want an SFU, primarily if you do not trust the machine the SFUs are operated on.

The semi-novel bit here is the backhaul from the assumed AP host to the cloud. The idea is this reduces load on the link between them, enabling different clients at each end to share the transported media streams.

![Diagram of dual SFU with backhaul between them](docs/sfu-diagram-2.svg?raw=true "Dual SFUs with backhaul")

There are possible extensions to this like making the cloud server purely signalling and connecting two APs at separate sites directly together, and even merging the network connection negotiation information so devices can dynamically switch which SFU to use based on what is optimal for their situation.

## How do I try it out?
It's fairly easy to build/run on a generic desktop, assuming you meet the security prerequisites and have the dependencies. The OpenWrt and Docker/traefik sections detail specific aspects of those environments.

It would be unwise to leave this running on a public network as is for very long, and I cannot be responsible for anything that would result! Try it at your own risk, ideally somewhere unlikely to be found.

### Security prerequisites
Using WebRTC features in a browser requires the browser to believe it is in a "secure context", which in practice for this means serving https. For the cloud side of things this is fairly easy since you can use something like letsencrypt in the normal way.

Running the SFU on a local subnet, such as that run by an AP, requires creating a key and certificate for the server so devices can keep track of the identity of the server. This is detailed in [KEYS_SETUP.md](KEYS_SETUP.md) . 

### Building
The main SFU is a golang project using the pion library for webrtc functionality. Protobufs are used for communication, and there is a small React/Typescript front end which gets bundled into the binary for ease of deployment. The main entry points for building are the scripts build_PROCESSOR_OS.sh scripts which use the golang cross compilation support to build for the different target architectures. The frontend project is built with esbuild and triggered via the npm commands in build_common.sh, along with the protoc compilation step.

To build you need:
* golang
* npm
* protoc, including the golang support

Then run one of the shell scripts, and it should produce an executable at ./umbrella .

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

#### Docker/traefik
For running on a public webserver something like docker compose with traefik will take away a lot of headaches. However, it introduces at least one big one. WebRTC uses a lot of ephemeral ports for communication, and these need to be exposed directly by the container. This means you have to run the container with host networking.

There are several environment variables which configure the SFU to run in cloud mode.
* UMBRELLA_CLOUD=1 - set to cloud mode
* UMBRELLA_HTTP_PREFIX - the path from the base url to the sfu, including forward slash, so "/umbrella" serves at https://domain:port/umbrella/sfu
* UMBRELLA_HTTP_SERVE_ADDR - the addr the server serves on, as host and port. To override just the default port 8081 set to :PORT, e.g. ":9000".
* UMBRELLA_PUBLIC_IP= - set to the public IP of the server. i.e. 245.234.244.122
* UMBRELLA_PUBLIC_HOST= - set to the public host of the server. i.e. www.atomirex.com
* UMBRELLA_MIN_PORT= , UMBRELLA_MAX_PORT= - set to the minimum and maximum ephemeral ports to allocate - e.g. UMBRELLA_MIN_PORT=50000, UMBRELLA_MAX_PORT=55000

The frontend is served on 8081, unless you override UMBRELLA_HTTP_SERVE_ADDR, and will need proxying for https for the public internet. You probably want to block whatever port you use from the public internet (here assumed to be on eth0) with something like:
```
iptables -A INPUT -p tcp --dport 8081 -i eth0 -j REJECT
```

This won't persist across reboots by default though, which can be done with the package iptables-persistent.

An example docker-compose file is in the deployment directory.

### Running it

#### Cloud
The easiest way to run it in the cloud is dockerized behind something like a traefik reverse proxy. This way traefik can act as the https endpoint, taking care of the certs, leaving the only real problem being ensuring you use host networking.

The docker-compose-example.yml in deployment shows how you can run it as a sub page of an existing domain.

When it's running you should be able to access it at https://DOMAIN/umbrella/sfu .

#### Locally
Running locally should be a question of running the umbrella executable, in a directory with the service.crt and service.key files created earlier (see [KEYS_SETUP.md](KEYS_SETUP.md) ). Port 8081 will need to be available and accessible. Then you can visit: https://HOSTNAME:8081/sfu

#### Connecting the two
The real party trick here is a joint session across SFUs. To do this access the server to push to the other (most likely local to cloud, so local), and visit https://HOSTNAME:8081/trunk . In the text field put the websocket address for the other server and press the button. The websocket address is "wsb" instead of "sfu", and the protocol is "wss" instead of "https". For example: wss://DOMAIN/umbrella/wsb To stop trunking clear the input field and press "Set trunk".

What should happen is all client data for each SFU is relayed to every client of both SFUs, as the result of the data going over the connection between the relays. 

 his is a hacked up UI, but it does work.

## How do I develop against it?
This is a real proof of concept mess. Any focused PRs or issues are welcome, as are forks. Assume zero stability at this stage.

The immediate priority is to get this out there to see what happens, and then work on adding "role" type functionality, primarily so signalling can be separated from the SFU so that two remote sites can directly bridge between them. Other things likely to happen soon are auth and adding an mqtt broker for IoT integrations.

If you're not into the whole multi-site aspect of it you're almost certainly better off with livekit or daily as mentioned at the top!

## What does it not do?
* Authentication - there isn't any at all. 
* Simulcast - a feature where clients upload streams at multiple different bitrates, and the SFU forwards on the most appropriate bitrate available for each client depending on the available bandwidth.
* "Pull" optimizations - right now media is forwarded to endpoints whether it is consumed there or not. For example, if you have backhaul to the cloud active all AP client media is forwarded to the cloud even if no clients are connected to the cloud instance.
* Cycles in backhaul will explode. It can deal with star topologies but because each node simply relays everything right now a cycle will go very wrong.