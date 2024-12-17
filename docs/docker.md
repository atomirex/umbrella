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