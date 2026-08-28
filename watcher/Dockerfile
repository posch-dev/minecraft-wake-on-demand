FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

ARG VERSION=docker
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/mc-wol-proxy .

FROM scratch

# DuckDNS is reached over HTTPS, which needs a trust store.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/mc-wol-proxy /mc-wol-proxy

ENV MC_WOL_CONFIG=/config.yml
ENV HOME=/

EXPOSE 25565

ENTRYPOINT ["/mc-wol-proxy"]
CMD ["run"]
