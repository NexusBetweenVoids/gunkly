# Updating Gunkly

Gunkly tells you when a new version is out (Administration, **Updates**). Updating means pulling the new image and restarting the container; your data stays in its volume and migrations run on start.

## By hand

```sh
docker compose pull && docker compose up -d
```

## With the "Update now" button (optional)

Gunkly cannot update itself, on purpose: replacing its own container would need access to Docker (the Docker socket), and that is the same as full control of the server. Anyone who broke into Gunkly would get that control too.

Instead, Gunkly can **ask an updater you run**. You give it the updater's address (and a token if it needs one) under Administration, **Updates**, **Update button**. When a new version is out, **Update now** sends that address one `POST` request (with `Authorization: Bearer <token>` when a token is set). The updater pulls the image and restarts Gunkly; the page waits and reloads when the new version answers. Gunkly holds nothing more than a token that can start an update.

You can also set it in the environment (`GUNKLY_UPDATE_WEBHOOK_URL`, `GUNKLY_UPDATE_WEBHOOK_TOKEN`); then the admin page shows it locked.

### Example: Watchtower

[Watchtower](https://nicholas-fedor.github.io/watchtower/http-api-mode) can wait for exactly this request. Add it next to Gunkly in the same compose file:

```yaml
services:
  gunkly:
    # ... as before, plus:
    labels:
      - com.centurylinklabs.watchtower.enable=true

  watchtower:
    image: nickfedor/watchtower
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      WATCHTOWER_HTTP_API_UPDATE: "true"
      WATCHTOWER_HTTP_API_TOKEN: "a-long-random-token"   # openssl rand -hex 24
      WATCHTOWER_LABEL_ENABLE: "true"                    # only containers with the label above
      WATCHTOWER_CLEANUP: "true"
```

Then in Gunkly, **Update button**: address `http://watchtower:8080/v1/update`, token the same `a-long-random-token`.

Notes:

- Watchtower itself has the Docker socket; keep it off any published port (the example publishes none, and only Gunkly on the same Docker network can reach it).
- In this mode Watchtower stops checking on a schedule; add `WATCHTOWER_HTTP_API_PERIODIC_POLLS: "true"` if you also want that.
- Check your Watchtower version's documentation for how it expects the token; Gunkly sends `Authorization: Bearer <token>`.
- An updater only pulls new images for the tag you run: with `ghcr.io/nexusbetweenvoids/gunkly:0.6` you get 0.6.x fixes, with `latest` every release.

### Other updaters

Anything that starts an update on a web request works: a redeploy webhook from your container manager, a CI job, or a small script of your own behind a reverse proxy. Addresses on your own network are allowed for this setting (cloud metadata addresses are not), and Gunkly does not follow redirects.

### What you see

- **accepted**: the updater answered with success.
- **sent**: no answer within 15 seconds; many updaters only answer when done, and that includes restarting Gunkly. The page keeps waiting.
- **refused** or **couldn't reach**: check the address and token, and that the updater runs.

Every request is recorded in the audit log (`admin_update_requested`).
