# One Dockerfile for all three services, selected with --build-arg SERVICE=...
# so they share a base image and build cache. Both gateway replicas run the
# exact same image, which is the point: replicas differ only in where they
# run, never in what they are.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICE
RUN test -n "$SERVICE" && CGO_ENABLED=0 go build -trimpath -o /out/app ./cmd/${SERVICE}

FROM alpine:3.22
RUN adduser -D -u 10001 app
COPY --from=build /out/app /usr/local/bin/app
COPY web /web
USER app
ENTRYPOINT ["/usr/local/bin/app"]
