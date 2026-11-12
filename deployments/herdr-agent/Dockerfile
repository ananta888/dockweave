# dockweave/herdr-agent — DockWeave agent image with the herdr runtime.
#
# Build (the herdr binary is copied from the host into the build context):
#   cp "$(command -v herdr)" deployments/herdr-agent/herdr
#   docker build -t dockweave/herdr-agent deployments/herdr-agent
#
# Use as the agent image with runtime "herdr":
#   dockweave agent create NAME --image dockweave/herdr-agent --runtime herdr --type codex
FROM node:22-bookworm

# herdr is a static-pie binary; drop it straight in.
COPY herdr /usr/local/bin/herdr
COPY entrypoint.sh /usr/local/bin/dockweave-herdr
RUN chmod +x /usr/local/bin/herdr /usr/local/bin/dockweave-herdr

# Harness (codex CLI) baked in so `herdr agent start --kind codex` finds it.
RUN npm install -g @openai/codex

WORKDIR /workspace
CMD ["/usr/local/bin/dockweave-herdr"]
