# syntax=docker/dockerfile:1

FROM golang:alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/ard-plus-dl ./cmd/ard-plus-dl

FROM alpine:3.22
RUN apk add --no-cache ffmpeg ca-certificates
COPY --from=build /out/ard-plus-dl /usr/bin/ard-plus-dl
# Convenience alias kept from the previous image.
RUN ln -s /usr/bin/ard-plus-dl /usr/bin/download

ENV DOWNLOADS_DIR=/data/downloads
ENV XDG_STATE_HOME=/data
# The credentials file lives at /data/ard-plus-dl/credentials, on the volume.
ENV XDG_CONFIG_HOME=/data
WORKDIR /data

ENTRYPOINT ["/usr/bin/ard-plus-dl"]
