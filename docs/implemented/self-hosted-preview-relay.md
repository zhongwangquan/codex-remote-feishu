# Self-Hosted Preview Relay

> Type: `implemented`
> Updated: `2026-07-28`
> Summary: 说明自托管 HTTPS 预览中继的服务端部署、本地 daemon 配置、协议边界与安全要求。

## Goal

Replace `trycloudflare` external exposure with a self-hosted HTTPS relay under your own domain.

Recommended deployment options:

- dedicated subdomain: `https://preview.flutterweb.cn`
- existing HTTPS domain with path prefix: `https://api.flutterweb.cn/codex-preview`

The local daemon keeps the preview service on loopback, and opens an outbound websocket tunnel to your own relay server. Public users only reach your server, not Cloudflare TryCloudflare.

## Server Deployment

Files:

- `deploy/preview-relay/docker-compose.yml`
- `deploy/preview-relay/Dockerfile`
- `deploy/preview-relay/Caddyfile`
- `deploy/preview-relay/.env.example`

Steps:

1. Either point DNS for `preview.flutterweb.cn` to your server, or choose an existing HTTPS domain and a path prefix such as `/codex-preview`.
2. Copy `.env.example` to `.env` and replace `PREVIEW_RELAY_SHARED_SECRET`.
3. If you deploy under a path prefix, set `PREVIEW_RELAY_BASE_PATH=/codex-preview`.
4. Start the relay:

```bash
cd deploy/preview-relay
docker compose up -d --build
```

5. Verify:

```bash
curl -fsS https://preview.flutterweb.cn/healthz
# or
curl -fsS https://api.flutterweb.cn/codex-preview/healthz
```

Expected response:

```text
ok
```

## Local Daemon Config

Set `externalAccess.provider.kind` to `selfhostedrelay`.

Example `config.json` fragment:

```json
{
  "externalAccess": {
    "listenHost": "127.0.0.1",
    "listenPort": 9512,
    "defaultLinkTTLSeconds": 600,
    "defaultSessionTTLSeconds": 1800,
    "provider": {
      "kind": "selfhostedrelay",
      "lazyStart": true,
      "selfHostedRelay": {
        "baseURL": "https://api.flutterweb.cn/codex-preview",
        "tunnelURL": "wss://api.flutterweb.cn/codex-preview/ws/tunnel",
        "sharedSecret": "replace-with-the-same-secret",
        "instanceID": "jason-local"
      }
    }
  }
}
```

Equivalent environment variables:

```bash
export CODEX_REMOTE_EXTERNAL_ACCESS_PROVIDER=selfhostedrelay
export CODEX_REMOTE_SELF_HOSTED_RELAY_BASE_URL=https://api.flutterweb.cn/codex-preview
export CODEX_REMOTE_SELF_HOSTED_RELAY_TUNNEL_URL=wss://api.flutterweb.cn/codex-preview/ws/tunnel
export CODEX_REMOTE_SELF_HOSTED_RELAY_SHARED_SECRET=replace-with-the-same-secret
export CODEX_REMOTE_SELF_HOSTED_RELAY_INSTANCE_ID=jason-local
```

## Scope

This change covers all external-access URLs issued by the daemon through:

- `preview`
- `review`
- `debug`

Those URLs now resolve under your own relay domain instead of `*.trycloudflare.com`.

## Current Limit

This relay currently proxies normal HTTP requests. Websocket upgrade relay is not enabled yet. If a future external-access flow requires websocket passthrough, add it on top of the same tunnel protocol.
