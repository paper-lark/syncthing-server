FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /syncthing-server ./cmd/syncthing-server \
    && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /oidc-fixture ./dev/oidc-fixture

FROM alpine:3.22 AS oidc-fixture
COPY --from=build /oidc-fixture /usr/local/bin/oidc-fixture
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/oidc-fixture"]

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 app && adduser -S -D -H -u 10001 -G app app
COPY --from=build /syncthing-server /usr/local/bin/syncthing-server
COPY dev/compose/config.json /opt/local/config.json
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/syncthing-server"]
