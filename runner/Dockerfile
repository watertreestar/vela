FROM oven/bun:1

WORKDIR /app
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl tar gzip bash docker-cli \
  && curl -fsSL https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=0.21.3 BIN_DIR=/usr/local/bin sh \
  && rm -rf /var/lib/apt/lists/*

COPY package-runner.js package.json ./
COPY dagger ./dagger
RUN bun install --production

ENV PACKAGING_API_URL=http://host.docker.internal:8080
CMD ["bun", "/app/package-runner.js"]
