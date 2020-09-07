# Websocket balancer

[![PkgGoDev](https://pkg.go.dev/badge/github.com/apundir/wsbalancer)](https://pkg.go.dev/github.com/apundir/wsbalancer)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/apundir/wsbalancer)](https://github.com/apundir/wsbalancer/releases)
[![GitHub license](https://img.shields.io/github/license/apundir/wsbalancer)](https://github.com/apundir/wsbalancer/blob/master/LICENSE)

Websocket balancer is a stateful reverse proxy for websockets. It distributes
incoming websockets across multiple available backends. In addition to load
balancing, the balancer also takes care of transparently switching from one
backend to another in case of mid session abnormal failure.

During this failover, the remote client connection is retained as-is thus
remote client do not even see this failover. Every attempt is made to ensure
none of the message is dropped during this failover.

The websocket balancer originated from OCPP (Open Charge Point Protocol)
world to manage websocket connections between EVSE (Electric Vehicle Supply
Equipment, a.k.a. charger) and Central Server. This version of websocket
balancer is a little stripped down version having NO OCPP extension or
knowledge what so ever and is designed to work in ANY websocket scenario
including that of OCPP.

The documenation of wsbalancer is activly being updated. Most of the updates 
will happen within godoc and as such will be available on godoc site as well
as within your favorite editor should you wish you use wsbalancer as a library.

## General overview


Following diagram illustrate the outline of the balancer.
<pre>
          ______________
          |              |
 Incoming |              |         +----> Backend 1
 Websocket|              |         |         :
     ---->|  Frontend    | ========|----> Backend 2
 Request  |              |         |         :
          |              |         +----> Backend N
          |              |
          |______________|
</pre>
At high level,  it is no different than any other reverse proxy. What makes
this balancer unique is set of following features.

1. Stateful failover from one backend to another. In event of an abnormal
termination of a connection from any backend, the frontend tries to make
another connection with still available backends. If new backend connection
is successfully established, frontend keeps on tunneling websocket messages
to this new backend. All this happens transparently and remote end of the
incoming frontend connection do not even see any disruption.

2. Remote administration over REST APIs. Administrative APIs are designed for
full manual control and DevOps automation without any downtime. In
production, things do go wrong and when they go wrong, you need tools at your
disposal using which you are able to take corrective actions without adding
any downtime to the system. Administrative APIs are designed specifically for
this purpose. You can enquire state of connections, disable/enable backends,
add/delete backends, terminate a session or perform a forceful failover of
any session. Remote administration is exposed via a separate HTTP server and
is designed for internal use only. The APIs accepts and generates JSON data
which is specifically designed for DevOps automation. These APIs are already
in use for Blue/Green deployment as well as A/B testing during upgrades
within DevOps automated workflows.

3. Runtime observability over prometheus. All data points are exposed as
promethus metrics. You can use prometheus+grafana to get detailed insights
into what's going on in the balancer and setup alerts as per your specific
business cases. Prometheus endpoint is exposed over administrative HTTP
interface.

4. Extensible at it's core. The frontend does not know or assume anything
about the backend system. Frontend only exposes interfaces which are expected
from any backend connection. It does not even assume that backend connection
is (or using) a websocket. As long as the interface contract is adhered to,
frontend can keep sending data to any backend (not just websocket). Although
this version of balancer has support for ONLY backend that inherently uses
websockets to talk to backend servers, the design allow to plug in any
backend you wish. You can build "Websocket to HTTP REST", "Websocket to
Redis" or "Websocket to RabbitMQ" bridge. you can plug in completely custom
protocol oriented backend system if you need and frontend will keep working
with these backends. SessionListener and MessageValve are other extension
points in the balancer which give you handle of data exchange and adjust so
as to build your custom message transformations, filters and/or observation
points.

5. Embeddable - the balancer core design is kept such that it can be embedded
in any other http server or reverse proxy easily. All configuration pieces of
core functionality is encapsulated in configuration types which can be built
programmatically and passed to balancer system components. Websocket balancer
is designed for specific objective and scope, it does not (and will not)
cross this boundary to become a full fledged general purpose http reverse
proxy. There are may good, very good solutions (many in go itself) out there
for that purpose. Having said that balancer can be easily integrated into any
of these solution nicely. Through code level integration (by using balancer
as a library) OR via redirecting websocket request from existing reverse
proxies to websocket balancer.

6. Designed for container - the balancer is designed with many of the
Twelve-Factor app principles. Entire configuration can be passed via
configuration file OR passed using environment variables thus providing
maximum flexibility for any deployment or automation scenario. Embed in
docker and run multiple pods in Kubernetes cluster to keep high availability
of the balancer. It is already being used in a Kubernetes cluster in
production.

## How to Use

You can launch balancer via command line. You can either pass the yml
configuration file OR set the environment variables before launch and
balancer will configure itself accordingly. You can mix and match these
configuration options as well where none/some/all of configuration is
supplied via configuration file and then specific overrides are made using
environment variables. Any value defined in configuration variables takes
higher precedence. Following is the example of wsbalancer CLI help text as
of now (please always refer to help o/p of latest balancer that you have
since this documentation may be out of date in this regard)

<pre>
$ wsbalancer --help
Usage of wsbalancer:
  -config string
        path of the configuration file for balancer
  -help
        Print help and exit
Environment variables:
  FE_RB_SIZE int
        WebSocket ReadBuffer size (bytes) (default "1024")
  FE_WB_SIZE int
        WebSocket WriteBuffer size (bytes) (default "1024")
  FE_TRUST_XFF bool
        Trust X-Forwarded-For header from clients (default "true")
  BLOCK_HEADERS slice
        Additional headers to block, comma separated
  RECONNECT_HDR string
        Header to include during reconnect (default "X-Balancer-Reconnect")
  ENABLE_INST bool
        Enable Prometheus instrumentation (default "false")
  LOG_LEVEL string
        Logging level (default "WARN")
  BE_URLS slice
        Backend URL endpoints, comma separated, APPENDS to existing backends
  BE_URL_IDS slice
        Respective backend URL IDs, comma separated
  BE_PING_TIME int64
        Backend webSocket ping time
  BE_RB_SIZE int
        Backend webSocket ReadBuffer size
  BE_WB_SIZE int
        Backend webSocket WriteBuffer size
  BE_BREAKER_THRESHOLD int
        Backend circuit breaker threshold
  BE_HANDSHAKE_TIMEOUT int64
        Backend webSocket handshaked timeout
  BE_PASS_HEADERS slice
        Headers to pass from B/E to F/E, comma separated
  BE_ABNORMAL_CLOSE_CODES slice
        Abnormal close codes from B/E, comma separated, OVERWRITE backend config codes
  FE_ADDR string
        Frontend server listen address (default ":8080")
  FE_READ_TIMEOUT int64
        Frontend server read timeout (default "10s")
  FE_WRITE_TIMEOUT int64
        Frontend server write timeout (default "10s")
  FE_MAX_HDR_BYTES int
        Frontend server Max Header Size (default "524288")
  ADM_ADDR string
        Admin server listen address (default ":8081")
  ADM_READ_TIMEOUT int64
        Admin server read timeout (default "10s")
  ADM_WRITE_TIMEOUT int64
        Admin server write timeout (default "10s")
  ADM_MAX_HDR_BYTES int
        Admin server Max Header Size (default "524288")
</pre>

The configuration can be supplied using yml file OR passed in via appropriate
environment variables as listed above. A minimalist's configuration file must
have at least one backend. Following is one such yml configuration file
example.

<pre>
upstream:
  backends:
    - url: ws:localhost:9090
    - url: ws:localhost:9091
</pre>

## When to Use (and when not to use)

You should use wsbalancer if you need persistent connection with remote end
with your server without worrying about flip-flop on your backends which are
actually serving the websocket requests. This is especially useful during
rolling upgrades. A general rule of thumb is, if websocket connection break
b/w remote end and your server is NOT desired that wsbalancer is the right
tool. For (almost) everything else, wsbalancer may be un-necessary.

If you are absolutely fine with remote end being disconnected and reconnected
to your server than any standard HTTP reverse proxy with ordinary load
balancing shall work just fine for your case. Do not add un-necessary
complexity in your deployment environment unless there's absolutely a need
to. Keeping things simple is almost always best solution.