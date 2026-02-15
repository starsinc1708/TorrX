FROM golang:1.25 AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/torrentstream ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates ffmpeg

WORKDIR /app

COPY --from=build /out/torrentstream /app/torrentstream
COPY docs /app/docs

ENV HTTP_ADDR=:8080
ENV TORRENT_DATA_DIR=/data

EXPOSE 8080

ENTRYPOINT ["/app/torrentstream"]
