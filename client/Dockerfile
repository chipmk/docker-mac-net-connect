FROM golang:1.17 as build

WORKDIR /build

COPY go.* .

RUN go mod download

COPY . .

RUN go build -o app main.go

FROM debian:11-slim

COPY --from=build /build/app .

CMD ["./app"]
