FROM golang:1.24 AS build

WORKDIR /build

COPY go.* .

RUN go mod download

COPY . .

RUN go build -o app main.go

FROM debian:trixie-slim

RUN set -ex && \
    apt-get update &&  \
    apt-get install -y iptables && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /build/app .

CMD ["./app"]
