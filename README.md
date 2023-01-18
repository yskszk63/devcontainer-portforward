# devcontainer-portforward

Port forward for devcontainers. (for non vscode environments.)

## Usage

### Launch server

Start a server that exposes the ports.

```
docker run --rm --network host --mount=type=volume,source=devcontainer-portforward,target=/data ghcr.io/yskszk63/devcontainer-portforward-server
```

NOTE: Communication between client and server is done via unix sockets on mounted volumes.

### Launch devcontainer

Start devcontainers with the devcontainers feature, which includes an agent that forwards ports through the server.

```
devcontainer up --workspace-folder . --additional-features '{"ghcr.io/yskszk63/devcontainer-portforward/devcontainer-portforward:0":{}}'
```

When listening for sockets in devcontainers, the server also listens on the same port.

## Limitation

- Currently does not respect `forwardPorts` in devcontainer.json.
- Works only with x86_64 Linux.

# License

[License](LICENSE)

# Author

[yskszk63](https://github.com/yskszk63)
