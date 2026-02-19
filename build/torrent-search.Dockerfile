FROM golang:1.25 AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/torrent-search ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=build /out/torrent-search /app/torrent-search

ENV HTTP_ADDR=:8090

EXPOSE 8090

ENTRYPOINT ["/app/torrent-search"]
