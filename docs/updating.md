# Updating Gunkly

Gunkly tells you when a new version is out (Administration, **Updates**). Updating means pulling the new image and restarting the container; your data stays in its volume and migrations run on start.

## By hand

```sh
docker compose pull && docker compose up -d
```

## With the "Update now" button (optional)

Gunkly cannot update itself, on purpose: replacing its own container would need access to Docker (the Docker socket), and that is the same as full control of the server. Anyone who broke into Gunkly would get that control too.

Instead, Gunkly can **ask an updater you run**. You give it the updater's address (and a token if it needs one) under Administration, **Updates**, **Update button**. When a new version is out, **Update now** sends that address one `POST` request (with `Authorization: Bearer <token>` when a token is set). The updater pulls the image and restarts Gunkly; the page waits and reloads when the new version answers. Gunkly holds nothing more than a token that can start an update.

Pick your updater (Watchtower, Portainer, Dockhand or Other) so the page shows the right hints. Only Dockhand changes the request: see below.

You can also set it in the environment (`GUNKLY_UPDATE_WEBHOOK_KIND` = `watchtower`, `portainer`, `dockhand` or `other`, `GUNKLY_UPDATE_WEBHOOK_URL`, `GUNKLY_UPDATE_WEBHOOK_TOKEN`); then the admin page shows it locked.

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

### Example: Portainer

Portainer can redeploy a stack or recreate a container when its webhook URL is called; the URL itself is the secret, so leave the token empty.

1. In Portainer, open Gunkly's stack (or container) and turn on its **webhook**; for a stack also turn on **Re-pull image** so it fetches the new version.
2. Copy the webhook URL (it looks like `https://<portainer>/api/stacks/webhooks/<id>` or `https://<portainer>/api/webhooks/<id>`).
3. In Gunkly: Updater **Portainer**, Updater address = that URL, no token.

Which webhooks your Portainer edition offers differs per version; check its documentation.

### Example: Dockhand

[Dockhand](https://dockhand.pro/manual/#api-reference) has an API that redeploys a compose stack. Gunkly calls it with `{"pull": true}`, so Dockhand pulls the newest images and recreates the stack.

1. In Dockhand, open your profile, **API tokens**, **Generate token** (it starts with `dh_`).
2. Note the stack name Gunkly runs in and the environment's ID.
3. In Gunkly: Updater **Dockhand**, Updater address `https://<dockhand>/api/stacks/<stack name>/deploy?env=<environment ID>`, Token = the `dh_…` token.

Dockhand runs the deploy as a job and answers right away, so Gunkly shows **accepted** and waits for the new version.

### Other updaters

Anything that starts an update on a web request works: a redeploy webhook from your container manager, a CI job, or a small script of your own behind a reverse proxy. Addresses on your own network are allowed for this setting (cloud metadata addresses are not), and Gunkly does not follow redirects.

### What you see

- **accepted**: the updater answered with success.
- **sent**: no answer within 15 seconds; many updaters only answer when done, and that includes restarting Gunkly. The page keeps waiting.
- **refused** or **couldn't reach**: check the address and token, and that the updater runs.

Every request is recorded in the audit log (`admin_update_requested`).
