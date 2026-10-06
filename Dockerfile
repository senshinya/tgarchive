# tgarchive 镜像：前端（Node）→ 测试与编译（Go）→ 运行（Alpine + 官方 telegram-bot-api）。
# 基础镜像一律 tag@digest；升级时 tag 与 digest 一起改。

# ---- 前端：产物与目标架构无关，固定在构建机架构上跑 ----
FROM --platform=$BUILDPLATFORM node:24.21.0-alpine3.24@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm test && npm run build

# ---- Go：在构建机架构上跑测试，再交叉编译到目标架构（CI 中两者相同）----
FROM --platform=$BUILDPLATFORM golang:1.26.8-alpine3.24@sha256:8ac98ca534ac3f51e1f420a1dd2c15e74c75cfa0f23f3ad27eb5d7236c349a0c AS build
ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/web.go ./web/web.go
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go test ./...
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w" -o /out/tgarchive ./cmd/tgarchive

# ---- 官方 Bot API 服务器：只取目标架构的二进制 ----
FROM aiogram/telegram-bot-api:10.3@sha256:50a9ed1f229930add49fd3aad5fa4119f269e40a9b364e6bd03c4d754fb9d950 AS botapi

# ---- 运行 ----
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
# telegram-bot-api 的 ELF NEEDED：libssl.so.3 libcrypto.so.3 libz.so.1 libstdc++.so.6 libgcc_s.so.1（+ musl）
# ffmpeg 为浏览器播不了的视频生成 H.264 兼容版；x86_64 另装 Intel 核显的 VAAPI 驱动（iHD）
RUN apk add --no-cache libssl3 libcrypto3 zlib libstdc++ libgcc ca-certificates tzdata ffmpeg \
 && if [ "$(apk --print-arch)" = x86_64 ]; then apk add --no-cache intel-media-driver; fi \
 && addgroup -S -g 10001 tgarchive \
 && adduser -S -D -H -u 10001 -G tgarchive -h /data -s /sbin/nologin tgarchive \
 && mkdir -p /data \
 && chown 10001:10001 /data
COPY --from=botapi /usr/local/bin/telegram-bot-api /usr/local/bin/telegram-bot-api
COPY --from=build /out/tgarchive /usr/local/bin/tgarchive
# 缺运行库时在这里失败，而不是上线后子进程起不来
RUN telegram-bot-api --version && ffprobe -version >/dev/null && ffmpeg -hide_banner -encoders | grep -q libx264
LABEL org.opencontainers.image.source="https://github.com/senshinya/tgarchive" \
      org.opencontainers.image.licenses="GPL-3.0-only"
USER 10001:10001
WORKDIR /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/tgarchive"]
