# Stage 1: Build
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags "-X github.com/allaspects/tokenman/internal/version.Version=${VERSION} \
              -X github.com/allaspects/tokenman/internal/version.GitCommit=${COMMIT} \
              -X github.com/allaspects/tokenman/internal/version.BuildDate=${DATE}" \
    -o /tokenman ./cmd/tokenman

# Stage 2: Runtime
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S tokenman && \
    adduser -S -G tokenman -h /home/tokenman tokenman

COPY --from=builder /tokenman /usr/local/bin/tokenman

RUN mkdir -p /data && chown tokenman:tokenman /data

USER tokenman
WORKDIR /home/tokenman

VOLUME ["/data"]

ENV TOKENMAN_SERVER_DATA_DIR=/data

EXPOSE 7677 7678

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://localhost:7677/health || exit 1

ENTRYPOINT ["tokenman"]
CMD ["start", "--foreground"]
