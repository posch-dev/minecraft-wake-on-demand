FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY internal/ ./internal/

ARG VERSION=docker
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/mcwod .

FROM scratch

# DuckDNS is reached over HTTPS, which needs a trust store.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/mcwod /mcwod

ENV MCWOD_CONFIG=/config.yml
ENV HOME=/

EXPOSE 25565

ENTRYPOINT ["/mcwod"]
CMD ["run"]
