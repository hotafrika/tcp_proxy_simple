# Build stage
FROM golang:1.19-alpine AS build

RUN apk --update --no-cache add make

WORKDIR /app
ADD . /app

RUN go mod download

RUN go build -o go-app cmd/main.go

FROM alpine:3.16.0

ENTRYPOINT ["/app/go-app", "-pprof", "-config", "config.json", "-loglevel", "0"]
WORKDIR /app
RUN apk --update --no-cache add ca-certificates tzdata && update-ca-certificates

COPY --from=build /app/go-app /app/go-app
COPY config.json /app/config.json
