FROM golang:1.24-alpine AS build

ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/relay ./cmd/relay

FROM alpine:3.22

RUN addgroup -S relay && adduser -S -G relay relay
COPY --from=build /out/relay /usr/local/bin/relay

USER relay
EXPOSE 8080
HEALTHCHECK --interval=20s --timeout=3s --retries=3 CMD wget -q -O - http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/relay"]
