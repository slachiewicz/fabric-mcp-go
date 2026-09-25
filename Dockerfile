# Used by goreleaser, which provides the per-platform binary in $TARGETPLATFORM/.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/fabmcp /usr/local/bin/fabmcp
ENTRYPOINT ["/usr/local/bin/fabmcp"]
CMD ["server", "start"]
