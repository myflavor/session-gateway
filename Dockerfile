# --- 后端：纯 Go，CGO_ENABLED=0 ---
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/session-gateway ./cmd/session-gateway

FROM ubuntu:24.04
# 用 ubuntu 而不是 alpine：agent 通过 session_bash 执行的是真实命令，
# busybox 没有 find -printf、grep -P 这些常用能力，失败还是静默的。
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      bash ca-certificates tzdata util-linux \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /data/session \
 && chmod 750 /data/session
COPY --from=build /out/session-gateway /usr/local/bin/session-gateway
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# 入口按 PUID/PGID 动态建 writer 与 reader：宿主机上的属主是谁，
# writer 就是谁，否则挂载进来的目录写不进去。
ENV MEMGATE_CONFIG=/etc/session-gateway.json
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD timeout 2 bash -c '</dev/tcp/127.0.0.1/8080' || exit 1
ENTRYPOINT ["docker-entrypoint.sh", "session-gateway"]
