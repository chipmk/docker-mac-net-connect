FROM golang:1.26 AS build

WORKDIR /build

COPY go.* .

RUN go mod download

COPY . .

RUN go build -o app main.go

FROM debian:trixie-slim

RUN apt-get update && apt-get install -y \
  iptables

COPY --from=build /build/app .

CMD ["./app"]
