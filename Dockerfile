FROM alpine:3.22

RUN apk add --no-cache bash curl ffmpeg yt-dlp jq util-linux perl

WORKDIR /app
COPY ard-plus-dl.sh ./
RUN chmod +x /app/ard-plus-dl.sh && ln -s /app/ard-plus-dl.sh /usr/bin/download

ENV DOWNLOADS_DIR=/data/downloads
ENV XDG_STATE_HOME=/data
# Credentials file lives at /data/ard-plus-dl/credentials, i.e. on the mounted volume.
ENV XDG_CONFIG_HOME=/data
WORKDIR /data

# Arguments are appended directly: docker run ard-plus-dl <url> runs the script.
ENTRYPOINT ["/app/ard-plus-dl.sh"]
