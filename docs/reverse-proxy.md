# Reverse proxy

Pocket Lookout speaks plain HTTP on one port and serves the UI and `/api/*` from the same binary. Set `BASE_URL` to the public URL and `SECURE_COOKIES=true` behind HTTPS.

## Caddy

```
lookout.example.com {
	reverse_proxy pocket-lookout:8080
}
```

## Traefik (labels)

```yaml
labels:
  - traefik.http.routers.lookout.rule=Host(`lookout.example.com`)
  - traefik.http.services.lookout.loadbalancer.server.port=8080
```

## nginx

```
location / {
    proxy_pass http://pocket-lookout:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    client_max_body_size 25m;
}
```

## Forward auth

With `AUTH_MODE=proxy` the app trusts `Remote-User` / `Remote-Email`. Exclude `/feed/*` (RSS readers have no login; the secret in the address is the key), plus `/api/v1/dashboard` and `/api/v1/watches` if a Homepage widget or a phone shortcut calls them with a bearer token.

## The camera scanner

Browsers only hand the camera to pages served over HTTPS (or `localhost`). Put the app behind TLS before expecting the barcode scanner to work on a phone.
