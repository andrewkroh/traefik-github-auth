FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETOS
ARG TARGETARCH
COPY ${TARGETOS}/${TARGETARCH}/traefik-github-auth /traefik-github-auth
ENTRYPOINT ["/traefik-github-auth"]
