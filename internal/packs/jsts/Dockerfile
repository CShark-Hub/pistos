# JS/TS pack image: Node, Oxlint and the oxc-parser facts worker. TypeScript and ESLint are always the
# repository's own copies, mounted read-only from its node_modules.
FROM node:24.21.0-bookworm-slim@sha256:d6aa754f16b3197301076f047b5def2f02ea1dbbc2ca920407d46d7ec7f87b20
WORKDIR /opt/pistos
RUN npm install --ignore-scripts --no-audit --no-fund oxc-parser@0.153.0 oxlint@1.87.0
COPY facts.mjs /opt/pistos/facts.mjs
WORKDIR /
