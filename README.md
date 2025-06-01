# Gopher in the middle proxy

## Overview

gitmproxy is a lightweight MITM (Man-In-The-Middle) HTTP cache proxy designed for
caching HTTP(S) traffic for slow internet/network connections.


## Configuration Environment Variables

The following environment variables can be used to configure gitmproxy:

| Variable           | Description                                             | Default   |
|--------------------|---------------------------------------------------------|-----------|
| `LISTEN_ADDR`      | Address and port for the proxy server to listen on      | `:8090`   |
| `CACHE_DIR`        | Directory where cache files are stored                  | `cache`   |
| `MAX_SIZE`         | Maximum total cache size (e.g., 10GB, 0 = unlimited)    | `10GB`    |
| `ENTRY_MAX_SIZE`   | Maximum size for a single cached response (e.g., 500MB) | `500MB`   |
| `ENTRY_TTL`        | Time-to-live for each cache entry (e.g., 1h, 0 = none)  | `1h`      |
| `ENABLE_LOGGING`   | Enable logging of cache operations (`true`/`false`)     | `true`    |
| `IGNORE_SERVER_CACHE_CONTROL` | Ignore cache control headers from the server (`true`/`false`) | `false`   |

## Getting Started

Here is an example of how to run gitmproxy using Docker Compose:

```yaml
services:
  gitmproxy:
    image: ghcr.io/bboehmke/gitmproxy:master
    ports:
      - "8090:8090"
    environment:
      LISTEN_ADDR: ":8090"
      CACHE_DIR: "cache"
      MAX_SIZE: "10GB"
      ENTRY_MAX_SIZE: "500MB"
      ENTRY_TTL: "1h"
      ENABLE_LOGGING: "true"
      IGNORE_SERVER_CACHE_CONTROL: "false"
    volumes:
      - ./cache:/cache
```

This will start gitmproxy on port 8090 with a persistent cache directory. Adjust environment variables and volume paths as needed for your setup.

## Prometheus Metrics Endpoint

gitmproxy exposes a Prometheus-compatible metrics endpoint at `/_gitmproxy_metrics`.

- You can access metrics by visiting `http://<proxy_host>:<proxy_port>/_gitmproxy_metrics`.
- This endpoint provides statistics such as HTTP request counts and cache performance.
- Integrate this endpoint with your Prometheus server for monitoring.
