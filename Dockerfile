FROM debian:bookworm-slim AS build

ARG GO_VERSION=1.25.1
ARG TARGETOS=linux
ARG TARGETARCH

ENV GOROOT=/usr/local/go
ENV GOPATH=/go
ENV PATH="${GOROOT}/bin:${GOPATH}/bin:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        build-essential \
    && rm -rf /var/lib/apt/lists/*

# Install the requested Go toolchain for the target architecture.
RUN arch="${TARGETARCH:-amd64}" && \
    case "$arch" in \
      amd64) go_arch=amd64 ;; \
      arm64) go_arch=arm64 ;; \
      *) echo "unsupported TARGETARCH: $arch" && exit 1 ;; \
    esac && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.${TARGETOS}-${go_arch}.tar.gz" -o /tmp/go.tgz && \
    rm -rf /usr/local/go && \
    tar -C /usr/local -xzf /tmp/go.tgz && \
    rm /tmp/go.tgz

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

# Copy remaining sources and build the server binary.
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/garmrd ./cmd/garmrd

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    libstdc++6 \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/garmrd /usr/local/bin/garmrd
COPY garmr.json ./garmr.json

# Provide an empty data dir that can be mounted as a volume.
RUN mkdir -p /app/data/raw_fit
VOLUME ["/app/data"]

EXPOSE 8765
ENTRYPOINT ["/usr/local/bin/garmrd"]
CMD ["-config", "/app/garmr.json"]
