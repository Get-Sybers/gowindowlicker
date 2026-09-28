# syntax=docker/dockerfile:1
# get-sybers/gowindowlicker — the Windows artefact dozen as one static binary
# (every parser a sub-tool; lick is the all-parsers sweep, the default).
# Built from the REPO ROOT: the image bakes the sibling gomount/ binary, which
# pulls a disk image's artefact sets in-container so the parsers run on the
# image (GOWINDOWLICKER_IMAGE) — no mount, no FUSE, no privilege.

ARG GO_VERSION=1.26
ARG TOOL_VERSION=0.2.0
ARG GODFIR_REVISION=unknown
ARG GODFIR_RELEASE=dev

FROM golang:${GO_VERSION}-alpine AS build
ARG TOOL_VERSION
WORKDIR /src/gowindowlicker
COPY gowindowlicker/go.mod gowindowlicker/go.sum ./
RUN go mod download
COPY gowindowlicker/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${TOOL_VERSION}" -o /gowindowlicker .
# build-stage sanity gate
RUN /gowindowlicker --version
# gore's bundled batch definition, with modes fixed here rather than inherited
# from the checkout, so uid 2000 can read it whatever umask the build host used.
RUN cp re/batch/default.reb /tmp/default.reb && \
    mkdir -m 0755 /batch && install -m 0644 /tmp/default.reb /batch/default.reb

# gomount: CGO-free static reader of the disk image (its go.mod pins the Go
# it needs; the toolchain downloads it when the base is older)
FROM golang:alpine AS gomount-build
ARG TOOL_VERSION
ENV GOTOOLCHAIN=auto
WORKDIR /src/gomount
COPY gomount/go.mod gomount/go.sum ./
RUN go mod download
COPY gomount/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${TOOL_VERSION}" -o /out/gomount .

FROM scratch
COPY --from=build /gowindowlicker /gowindowlicker
COPY --from=build /batch /batch
COPY --from=gomount-build /out/gomount /usr/local/bin/gomount
COPY <<HARDENED /etc/dfir-hardened
schema=1
tool=gowindowlicker
user=dfir uid=2000 gid=2000
static_binary=true
shell=false python=false pkg_mgr=false
HARDENED
ARG DFIR_UID=2000
ARG DFIR_GID=2000
ARG TOOL_VERSION
ARG GODFIR_REVISION
ARG GODFIR_RELEASE
ENV HOME=/tmp XDG_CACHE_HOME=/tmp/.cache
USER ${DFIR_UID}:${DFIR_GID}
ENTRYPOINT ["/gowindowlicker"]
LABEL org.opencontainers.image.title="get-sybers/gowindowlicker" \
      org.opencontainers.image.description="The Windows artefact dozen as one structured binary: every Windows Go parser (prefetch, ESE/SRUM, Recycle Bin, \$MFT, Amcache, ShimCache, evtx, registry, ShellBags, .lnk, jump lists, Windows Timeline) embedded as a sub-tool, plus lick — the sweep that runs every parser over one evidence tree. Env-driven multi-tool entrypoint; FROM scratch, runs as uid 2000." \
      org.opencontainers.image.source="https://github.com/Get-Sybers/GoDFIR-toolz" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${TOOL_VERSION}" \
      org.opencontainers.image.revision="${GODFIR_REVISION}" \
      com.get-sybers.tool="gowindowlicker" \
      com.get-sybers.hardened="true" \
      com.get-sybers.contract="1" \
      com.get-sybers.godfir-release="${GODFIR_RELEASE}" \
      com.get-sybers.upstream="https://github.com/Get-Sybers/GoDFIR-toolz/tree/main/gowindowlicker"
