# Umbrella

Demo video: https://x.com/atomirex/status/1863956802984984728

This is a hacked up proof of concept WebRTC SFU that can run on many things, such as a local OpenWrt AP or in the cloud with Docker Compose/Traefik, including having a group session that spans both at once by backhauling from the AP to the cloud, and ingesting video from security cameras. Today it serves to help explore the problem space more than being any solution to anything.

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

Running the SFU on a local subnet, such as that run by an AP, requires creating a key and certificate for the server so devices can keep track of the identity of the server. This is detailed in [docs/KEYS_SETUP.md](docs/KEYS_SETUP.md) . 

### Building
The main SFU is a golang project using the pion library for webrtc functionality. Protobufs are used for communication, and there is a small React/Typescript front end which gets bundled into the binary for ease of deployment. The main entry points for building are the scripts build_PROCESSOR_OS.sh scripts which use the golang cross compilation support to build for the different target architectures. The frontend project is built with esbuild and triggered via the npm commands in build_common.sh, along with the protoc compilation step.

To build you need:
* golang
* npm
* protoc, including the golang support

Then run one of the shell scripts, and it should produce an executable at ./umbrella .

#### OpenWrt
One of the main targets for this is access points running OpenWrt. More details are in [docs/openwrt.md](docs/openwrt.md) .

#### Docker/traefik
For running on a public webserver something like docker compose with traefik will take away a lot of headaches. More details are in [docs/docker.md](docs/docker.md) .

### Running it

#### Locally
Running locally should be a question of running the umbrella executable, in a directory with the service.crt and service.key files created earlier (see [docs/KEYS_SETUP.md](docs/KEYS_SETUP.md) ). Port 8081 will need to be available and accessible. Then you can visit: https://HOSTNAME:8081/sfu

#### Cloud
The easiest way to run it in the cloud is dockerized behind something like a traefik reverse proxy. This way traefik can act as the https endpoint, taking care of the certs, leaving the only real problem being ensuring you use host networking.

The docker-compose-example.yml in deployment shows how you can run it as a sub page of an existing domain.

When it's running you should be able to access it at https://DOMAIN/umbrella/sfu .

#### Connecting the two
The real party trick here is a joint session across SFUs. To do this access the server to push to the other (most likely local to cloud, so local), and visit https://HOSTNAME:8081/servers . In the text field put the websocket address for the other server and press the button. The websocket address is "wsb" instead of "sfu", and the protocol is "wss" instead of "https". For example: wss://DOMAIN/umbrella/wsb To stop trunking remove the server connection.

What should happen is all client data for each SFU is relayed to every client of both SFUs, as the result of the data going over the connection between the relays. 

This is a hacked up UI, but it does work.

#### Using RTSP for cameras
Assuming you can access the rtsp feed of a camera (verifiable using VLC) you can ingest from the camera. Different camera brands are more/less reliable for this, and the whole feature is highly experimental, creating a whole load of new problems.

A RTSP camera acts as a server, so to use it you add a server on the same page as connecting between SFUs, i.e. https://HOSTNAME:8081/servers The format is something like rtsp://yourcameraname:yourcamerapassword@CAMERAIP/stream1 but it will vary based on your camera brand.

This only brings in video for now.

## How do I develop against it?
This is a real proof of concept mess. Any focused PRs or issues are welcome, as are forks. Assume zero stability at this stage.

The immediate priority is to get this out there to see what happens, and then work on adding "role" type functionality, primarily so signalling can be separated from the SFU so that two remote sites can directly bridge between them. Other things likely to happen soon are auth and adding an mqtt broker for IoT integrations.

If you're not into the whole multi-site aspect of it you're almost certainly better off with livekit or daily as mentioned at the top!

## What does it not do?
* Authentication - there isn't any at all. 
* Simulcast - a feature where clients upload streams at multiple different bitrates, and the SFU forwards on the most appropriate bitrate available for each client depending on the available bandwidth.
* "Pull" optimizations - right now media is forwarded to endpoints whether it is consumed there or not. For example, if you have backhaul to the cloud active all AP client media is forwarded to the cloud even if no clients are connected to the cloud instance.
* Cycles in backhaul will explode. It can deal with star topologies but because each node simply relays everything right now a cycle will go very wrong.