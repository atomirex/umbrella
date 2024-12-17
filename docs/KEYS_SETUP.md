# Keys setup
This uses WebRTC features of web browsers. For the API for this to work browsers must be persuaded they are operating in a secure context. https://developer.mozilla.org/en-US/docs/Web/Security/Secure_Contexts

## Cloud deployments
For securing the cloud deployment the easiest way is to deploy a docker container behind something like https://github.com/traefik/traefik configured to use https://letsencrypt.org as the certificate authority. The docker-compose-example.yml in deployment shows how to use a traefik wide certificate resolver as is normal for such things.

## Local subnetworks
Some notes in advance:
* For OpenWrt servers having all lower case host names helps avoid problems with umdns.
* Recent versions of desktop Chrome seem to completely ignore mdns. You can work around this by using the actual device IP instead, and going through the security warnings. (To get a device IP from a Mac open a terminal and run ```dns-sd -q target-machine-name.local``` and it will list the IPs of that device on your subnet).

### Server setup
You will need to know the host name of each server device.

Create a private key for this server:
```
openssl genrsa -out service.key 2048
```

Create a server certificate:
```
openssl req -x509 -new -key service.key -sha256 -days 365 -out service.crt
```

When prompted you will need to enter the host name and domain as the "Common Name" field, meaning it should be all lower case and with ".local" at the end. For example, for a machine called "Atomirex-Machine" enter "atomirex-machine.local". (OpenWrt servers should have hostnames all lowercase because the umdns responder may not respond properly).

The resulting service.crt and service.key files need to be in the directory alongside umbrella when it is launched.

The local subnetwork mode sets up the sfu page to serve at /sfu .

### Client setup
When the server is run it will output a line equivalent to "Starting server at https://atomirex-machine.local:8081/" which tells you the base url. The actual sfu is at /sfu for local deployments, and in the cloud likely /umbrella/sfu if following the default docker setup.

For cloud only deployments no client specific incantations are needed. For local subnetworks some of the notes here might help:

#### Safari
Vist the page https://[[hostname:port]]/sfu . The first time you will get "This Connection Is Not Private". Select "Show Details" then "Visit Website". It should load successfully.
#### Firefox (all platforms)
In Settings disable DNS over HTTPS, then assuming your operating system is setup to use mdns properly you should be able to vist the page at the url. It will tell you it's not secure - go through more details and visit anyway, after which it will work.
#### Android
You need to disable "Private DNS" in system settings. (Easiest to search for DNS). Set it to "Off".

Visit the page. Press "Show Advanced" and click on "Proceed to . . . " and it will load as normal.