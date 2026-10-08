# Python pack image: Python, Ruff, Pyright (with the Node runtime it needs) and the facts worker.
FROM node:24.21.0-bookworm-slim@sha256:d6aa754f16b3197301076f047b5def2f02ea1dbbc2ca920407d46d7ec7f87b20 AS node
RUN npm install -g --ignore-scripts --no-audit --no-fund pyright@1.1.414

FROM python:3.12.12-slim-bookworm@sha256:593bd06efe90efa80dc4eee3948be7c0fde4134606dd40d8dd8dbcade98e669c
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules/pyright /opt/pyright
RUN pip install --no-cache-dir --disable-pip-version-check ruff==0.16.10
COPY facts.py /opt/pistos/facts.py
