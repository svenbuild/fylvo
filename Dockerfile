# The Fylvo image. The server binary is built outside (scripts/build.sh), one per architecture:
# release/fylvo-linux-amd64 and release/fylvo-linux-arm64.
FROM alpine:latest

WORKDIR /fylvo

# Thumbnails and media info for images, videos, RAW photos and office files; aria2 for remote downloads.
RUN apk add --no-cache tzdata vips-tools ffmpeg libreoffice aria2 supervisor font-noto font-noto-cjk libheif libraw-tools \
    && mkdir -p ./data/temp/aria2 \
    && chmod -R 766 ./data/temp/aria2

ENV TZ=UTC \
    CR_ENABLE_ARIA2=1 \
    CR_SETTING_DEFAULT_thumb_ffmpeg_enabled=1 \
    CR_SETTING_DEFAULT_thumb_vips_enabled=1 \
    CR_SETTING_DEFAULT_thumb_libreoffice_enabled=1 \
    CR_SETTING_DEFAULT_media_meta_ffprobe=1 \
    CR_SETTING_DEFAULT_thumb_libraw_enabled=1

# Set by docker buildx; the default is for a plain "docker build".
ARG TARGETARCH=amd64
COPY .build/aria2.supervisor.conf .build/entrypoint.sh ./
COPY release/fylvo-linux-${TARGETARCH} ./fylvo

# Installations from before the rename stored absolute paths /cloudreve/data/... in the database
# (storage policy, files, thumbnails). The link keeps them working.
RUN chmod +x ./fylvo ./entrypoint.sh     && ln -s /fylvo /cloudreve

EXPOSE 5212 6888 6888/udp

VOLUME ["/fylvo/data"]

ENTRYPOINT ["sh", "./entrypoint.sh"]
