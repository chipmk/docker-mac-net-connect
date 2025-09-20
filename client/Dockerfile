FROM golang:1.24 AS build

WORKDIR /build

COPY go.* .

RUN go mod download

COPY . .

RUN go build -o app main.go

FROM debian:12-slim

RUN apt-get update && apt-get install -y \
  iptables

COPY --from=build /build/app .

CMD ["./app"]
